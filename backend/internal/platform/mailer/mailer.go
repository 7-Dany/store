// Package mailer provides SMTP email delivery for transactional messages.
package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"html/template"
	"io"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"

	"github.com/google/uuid"

	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	"github.com/7-Dany/store/backend/internal/platform/telemetry"
)

var log = telemetry.New("mailer")

// ErrSendFailed is a sentinel error returned by test doubles.
var ErrSendFailed = fmt.Errorf("mailer: send failed")

// uuidNewRandom is the uuid generation function used by buildMessage.
// Declared as a variable so tests can substitute a failing implementation.
var uuidNewRandom = uuid.NewRandom

// newBodyWriter constructs the quoted-printable writer used by buildMessage.
// Declared as a variable so tests can substitute a failing implementation.
var newBodyWriter func(io.Writer) io.WriteCloser = func(w io.Writer) io.WriteCloser {
	return quotedprintable.NewWriter(w)
}

// dialFunc performs the TCP dial inside sendWithContext.
// Declared as a variable so tests can inject a net.Pipe connection.
var dialFunc = func(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{}
	return d.DialContext(ctx, "tcp", addr)
}

// parsedEntry pairs a compiled template with its email subject format string.
// Both pieces of data belong to the same email type and are always used together.
type parsedEntry struct {
	tmpl       *template.Template
	subjectFmt string // e.g. "Verify your %s account"
}

// OTPHandlerBase is the single mail-delivery base for every OTP handler.
// Embed it in the Handler struct. Populate Send with the concrete *SMTPMailer
// method for this email flow (e.g. deps.Mailer.SendPasswordResetEmail).
//
// When Queue is non-nil, SendOTPEmail delivers asynchronously via the queue.
// When Queue is nil, Send is called synchronously — the preferred path in tests.
type OTPHandlerBase struct {
	Send    func(ctx context.Context, toEmail, code string) error
	Queue   *Queue
	Timeout time.Duration
}

// Config holds SMTP connection parameters.
// Port: 587 (STARTTLS) or 25 (internal/unauthenticated relay).
// Port 465 (SMTPS / implicit TLS) is NOT supported; sendWithContext performs
// a plaintext dial and upgrades via STARTTLS. Using 465 will produce a
// confusing timeout or garbled-greeting error.
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	AppName  string
	// OTPValidMinutes is the TTL displayed in OTP email bodies ("expires in N minutes").
	// It must match the INTERVAL literal in CreateEmailVerificationToken,
	// CreatePasswordResetToken, and CreateUnlockToken in sql/queries/auth/auth.sql.
	// Sourced from config.Config.OTPValidMinutes (env: OTP_VALID_MINUTES, default 15).
	// The database cap is 30 minutes (chk_ott_ev_ttl_max in sql/schema/001_core.sql).
	OTPValidMinutes int
}

// SMTPMailer sends transactional email over SMTP with STARTTLS support.
type SMTPMailer struct {
	cfg     Config
	auth    smtp.Auth
	entries map[string]parsedEntry
}

// New returns a ready-to-use SMTPMailer using PLAIN auth.
func New(cfg Config) (*SMTPMailer, error) {
	return NewWithAuth(cfg, smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host))
}

// NewWithAuth creates an SMTPMailer with a custom smtp.Auth implementation.
// Pass nil auth for unauthenticated servers (e.g. in-process test listeners).
// OTPValidMinutes controls the TTL displayed in the email body; it must match
// the INTERVAL baked into CreateEmailVerificationToken / CreatePasswordResetToken
// in sql/queries/auth/auth.sql.
// Accepted values: 0 (defaults to 15) or any value in [1, 30].
// The database cap is 30 minutes (chk_ott_ev_ttl_max).
func NewWithAuth(cfg Config, auth smtp.Auth) (*SMTPMailer, error) {
	if cfg.OTPValidMinutes == 0 {
		cfg.OTPValidMinutes = 15
	} else if cfg.OTPValidMinutes < 1 || cfg.OTPValidMinutes > 30 {
		return nil, fmt.Errorf("mailer: OTPValidMinutes must be between 1 and 30 (or 0 to use the default of 15), got %d", cfg.OTPValidMinutes)
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("mailer: cfg.From must not be empty") //nolint:goerr113
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return nil, fmt.Errorf("mailer: cfg.From %q is not a valid RFC 5322 address: %w", cfg.From, err)
	}
	m := &SMTPMailer{cfg: cfg, auth: auth, entries: make(map[string]parsedEntry)}
	for key, e := range mailertemplates.Registry() {
		t, err := template.New(key).Parse(*e.HTML)
		if err != nil {
			return nil, fmt.Errorf("mailer: parse %s template: %w", key, err)
		}
		m.entries[key] = parsedEntry{tmpl: t, subjectFmt: e.SubjectFmt}
	}
	return m, nil
}

// NewWithCustomTemplate creates an SMTPMailer with an injected verification
// template. Used in tests to exercise the template.Execute error branch.
// All other email types are loaded from the registry normally.
func NewWithCustomTemplate(cfg Config, auth smtp.Auth, tmpl *template.Template) *SMTPMailer {
	m := &SMTPMailer{cfg: cfg, auth: auth, entries: make(map[string]parsedEntry)}
	for key, e := range mailertemplates.Registry() {
		m.entries[key] = parsedEntry{
			tmpl:       template.Must(template.New(key).Parse(*e.HTML)),
			subjectFmt: e.SubjectFmt,
		}
	}
	// Override the verification entry with the injected template, keeping the subject.
	m.entries[mailertemplates.VerificationKey] = parsedEntry{
		tmpl:       tmpl,
		subjectFmt: mailertemplates.Registry()[mailertemplates.VerificationKey].SubjectFmt,
	}
	return m
}

// otpTplData is the shared template data struct for all OTP emails.
type otpTplData struct {
	AppName   string
	Code      string
	ValidMins int
	Year      int
}

// Send returns a delivery func bound to the given email-type key.
// The returned func satisfies the OTPHandlerBase.Send and Job.Deliver signatures,
// so callers pass it directly without naming the underlying template or subject:
//
//	mailer.OTPHandlerBase{Send: deps.Mailer.Send(templates.VerificationKey), ...}
func (m *SMTPMailer) Send(key string) func(context.Context, string, string) error {
	return func(ctx context.Context, toEmail, code string) error {
		return m.sendOTPEmail(ctx, toEmail, code, key)
	}
}

// sendOTPEmail looks up the entry for key, renders its template, builds an
// RFC 5322 message, and delivers it over SMTP.
func (m *SMTPMailer) sendOTPEmail(ctx context.Context, toEmail, code, key string) error {
	e := m.entries[key]
	subject := fmt.Sprintf(e.subjectFmt, m.cfg.AppName)
	toEmail = sanitiseHeader(toEmail)

	var buf bytes.Buffer
	if err := e.tmpl.Execute(&buf, otpTplData{
		AppName:   m.cfg.AppName,
		Code:      code,
		ValidMins: m.cfg.OTPValidMinutes,
		Year:      time.Now().Year(),
	}); err != nil {
		return telemetry.Mailer("sendOTPEmail.render_template", err)
	}

	msg, err := buildMessage(m.cfg.Host, m.cfg.From, toEmail, subject, buf.String())
	if err != nil {
		return telemetry.Mailer("sendOTPEmail.build_message", err)
	}

	addr := fmt.Sprintf("%s:%d", m.cfg.Host, m.cfg.Port)
	log.Debug(ctx, "dialing SMTP",
		"addr", addr,
		"from", m.cfg.From,
		"to", emailToken(toEmail),
		"subject", subject,
		"template", key,
	)
	if err := sendWithContext(ctx, addr, m.auth, m.cfg.From, []string{toEmail}, msg); err != nil {
		log.Error(ctx, "SMTP send failed",
			"addr", addr,
			"to", emailToken(toEmail),
			"template", key,
			"error", err,
		)
		return telemetry.Mailer("sendOTPEmail.smtp_send", err)
	}
	log.Debug(ctx, "SMTP send succeeded", "to", emailToken(toEmail), "template", key)
	return nil
}

// sendWithContext dials addr and performs the full SMTP exchange while
// honouring ctx. Cancellation during the dial is handled by DialContext.
// Cancellation after the dial is handled by closing the underlying conn,
// which unblocks any in-progress SMTP read or write.
//
// The internal goroutine that watches ctx.Done is bounded: it exits as soon
// as sendWithContext returns (via the deferred close(stopDeadline)).
func sendWithContext(ctx context.Context, addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("mailer.sendWithContext: invalid addr %q: %w", addr, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	conn, err := dialFunc(ctx, addr)
	if err != nil {
		return err
	}

	stopDeadline := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stopDeadline:
		}
	}()
	defer close(stopDeadline)

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Quit() //nolint:errcheck

	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return telemetry.Mailer("sendWithContext.starttls", err)
		}
	}
	if a != nil {
		if err := c.Auth(a); err != nil {
			return telemetry.Mailer("sendWithContext.smtp_auth", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	if _, err = wc.Write(msg); err != nil {
		return err
	}
	if err := wc.Close(); err != nil {
		return err
	}
	// Surface a context cancellation that raced with a successful send.
	return ctx.Err()
}

// sanitiseHeader removes characters used in header injection attacks.
func sanitiseHeader(s string) string {
	return strings.NewReplacer(
		"\r", "",
		"\n", "",
		"\x00", "",
		"\u2028", "",
		"\u2029", "",
		"\u0085", "", // NEL — treated as line break by some MIME decoders
		"\x0B", "", // VT  — treated as whitespace/line-break by legacy MUAs
		"\x0C", "", // FF  — same as VT in several MUA parsers
	).Replace(s)
}

// buildMessage assembles an RFC 5322 / MIME email message with a
// quoted-printable encoded HTML body. host is used as the domain portion
// of the Message-ID per RFC 5322 §3.6.4.
func buildMessage(host, from, to, subject, htmlBody string) ([]byte, error) {
	msgID, err := uuidNewRandom()
	if err != nil {
		return nil, telemetry.Mailer("buildMessage.generate_message_id", err)
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\n", sanitiseHeader(from))
	fmt.Fprintf(&buf, "To: %s\r\n", sanitiseHeader(to))
	fmt.Fprintf(&buf, "Subject: %s\r\n", sanitiseHeader(subject))
	fmt.Fprintf(&buf, "Date: %s\r\n", time.Now().UTC().Format("Mon, 02 Jan 2006 15:04:05 +0000"))
	fmt.Fprintf(&buf, "Message-ID: <%s@%s>\r\n", msgID, sanitiseHeader(host))
	fmt.Fprintf(&buf, "MIME-Version: 1.0\r\n")
	fmt.Fprintf(&buf, "Content-Type: text/html; charset=UTF-8\r\n")
	fmt.Fprintf(&buf, "Content-Transfer-Encoding: quoted-printable\r\n")
	fmt.Fprintf(&buf, "\r\n")

	qpw := newBodyWriter(&buf)
	if _, err := qpw.Write([]byte(htmlBody)); err != nil {
		return nil, telemetry.Mailer("buildMessage.qp_encode_body", err)
	}
	if err := qpw.Close(); err != nil {
		return nil, telemetry.Mailer("buildMessage.qp_close", err)
	}
	return buf.Bytes(), nil
}

// sendOTP is the shared private engine for async/sync OTP email delivery.
// deliver is the send function that maps to the right email template.
//
// Synchronous-fallback caution: when q is non-nil but full or shut down,
// sendOTP falls back to synchronous delivery, holding a goroutine for up to
// timeout. Under a sustained SMTP outage this fallback can multiply: every
// concurrent request that reaches sendOTP blocks for the full timeout duration.
// If the queue is persistently exhausted, callers should pass a nil queue and
// implement a circuit-breaker above this call.
func sendOTP(
	ctx context.Context,
	q *Queue,
	deliver func(context.Context, string, string) error,
	userID, email, rawCode, logPrefix string,
	timeout time.Duration,
) error {
	// Anti-enumeration: a zero RawCode signals the service suppressed this
	// request (unknown email / already-verified / cooldown path). Return
	// immediately without sending mail so callers don't need their own guard.
	if rawCode == "" {
		log.Debug(ctx, logPrefix+": sendOTP suppressed — empty rawCode (anti-enumeration)")
		return nil
	}

	// Async path — create a context bounded by the timeout but NOT connected to
	// a cancel function in this goroutine. The queue worker owns the context.
	if q != nil {
		deadline := time.Now().Add(timeout)
		asyncCtx, asyncCancel := context.WithDeadline(context.Background(), deadline)
		_ = asyncCancel

		if err := q.Enqueue(Job{
			Ctx:     asyncCtx,
			UserID:  userID,
			Email:   email,
			Code:    rawCode,
			Deliver: deliver,
		}); err == nil {
			return nil
		} else {
			asyncCancel()
			log.Warn(ctx, logPrefix+": queue enqueue failed, falling back to sync delivery",
				"error", err, "email_token", emailToken(email))
		}
	}

	// Synchronous fallback — cancel when this call returns.
	syncCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := deliver(syncCtx, email, rawCode); err != nil {
		log.Error(ctx, logPrefix+": sync mail delivery failed", "error", err, "email_token", emailToken(email))
		return err
	}
	return nil
}

// SendOTPEmail delivers an OTP email via base.Queue (async) or base.Send
// (synchronous fallback). It replaces SendOTP, SendUnlockOTP, and
// SendPasswordResetOTP. All OTP handlers call this one function.
func SendOTPEmail(
	ctx context.Context,
	base OTPHandlerBase,
	userID, email, rawCode, logPrefix string,
) error {
	return sendOTP(ctx, base.Queue, base.Send, userID, email, rawCode, logPrefix, base.Timeout)
}
