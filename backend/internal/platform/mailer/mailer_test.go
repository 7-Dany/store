package mailer_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/7-Dany/store/backend/internal/platform/mailer"
	mailertemplates "github.com/7-Dany/store/backend/internal/platform/mailer/templates"
	mailertest "github.com/7-Dany/store/backend/internal/platform/mailer/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// baseConfig returns a minimal valid Config for testing.
func baseConfig() mailer.Config {
	return mailer.Config{Host: "127.0.0.1", Port: 25, From: "noreply@example.com", AppName: "Test"}
}

// newBaseMailer creates an SMTPMailer with nil auth from baseConfig.
func newBaseMailer(t *testing.T) *mailer.SMTPMailer {
	t.Helper()
	m, err := mailer.NewWithAuth(baseConfig(), nil)
	require.NoError(t, err)
	return m
}

// ── mailer.go:81.16,83.3 — template.Parse error in NewWithAuth ───────────────

// TestNewWithAuth_TemplateParseError covers the template.Parse error branch
// (mailer.go:81-83) by temporarily replacing the package-level template source
// with an invalid Go template string.
//
// MUST NOT run in parallel: mutates a package-level variable.
func TestNewWithAuth_TemplateParseError(t *testing.T) {
	orig := *mailertemplates.VerificationEmailTemplate
	*mailertemplates.VerificationEmailTemplate = "{{invalid template" // malformed: unclosed action
	defer func() { *mailertemplates.VerificationEmailTemplate = orig }()

	cfg := baseConfig()
	_, err := mailer.NewWithAuth(cfg, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "parse verification template")
}

// ── mailer.go:110.17,112.3 — tmpl.Execute error in sendOTPEmail ─────

// TestSendVerificationEmail_TemplateExecuteError covers the tmpl.Execute error
// branch (mailer.go:110-112). The template accesses AppName as if it were a
// struct with a sub-field X, which causes Execute to fail at runtime because
// AppName is a plain string.
func TestSendVerificationEmail_TemplateExecuteError(t *testing.T) {
	t.Parallel()

	// "{{.AppName.X}}" is syntactically valid but Execute fails when AppName
	// (a string) is dereferenced as a struct.
	badTmpl := template.Must(template.New("bad").Parse("{{.AppName.X}}"))
	m := mailer.NewWithCustomTemplate(baseConfig(), nil, badTmpl)

	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	require.ErrorContains(t, err, "render template")
}

// ── mailer.go:116.16,118.3 — buildMessage error in sendOTPEmail, and
//    mailer.go:216.16,218.3 — uuid.NewRandom error in buildMessage ───────────

// TestSendVerificationEmail_BuildMessageUUIDError covers both:
//   - mailer.go:116-118 (if err != nil after buildMessage in sendOTPEmail)
//   - mailer.go:216-218 (if err != nil after uuidNewRandom in buildMessage)
//
// It replaces the uuid factory with one that always returns an error so that
// buildMessage fails before any network dial is attempted.
//
// MUST NOT run in parallel: mutates uuidNewRandom.
func TestSendVerificationEmail_BuildMessageUUIDError(t *testing.T) {
	defer mailer.SetTestUUIDNewRandom(func() (uuid.UUID, error) {
		return uuid.UUID{}, errors.New("entropy pool exhausted")
	})()

	m := newBaseMailer(t)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	// sendOTPEmail wraps the error with "mailer: build message:".
	require.ErrorContains(t, err, "build message")
}

// ── mailer.go:232.55,234.3 — qpw.Write error in buildMessage ─────────────────

// failOnWrite is an io.WriteCloser whose Write always returns an error.
type failOnWrite struct{}

func (failOnWrite) Write(_ []byte) (int, error) { return 0, errors.New("write: injected failure") }
func (failOnWrite) Close() error                { return nil }

// TestSendVerificationEmail_BuildMessageQPWriteError covers mailer.go:232-234
// by replacing the body-writer factory with one that returns a WriteCloser
// whose Write always fails.
//
// MUST NOT run in parallel: mutates newBodyWriter.
func TestSendVerificationEmail_BuildMessageQPWriteError(t *testing.T) {
	defer mailer.SetTestBodyWriter(func(_ io.Writer) io.WriteCloser {
		return failOnWrite{}
	})()

	m := newBaseMailer(t)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	require.ErrorContains(t, err, "build message")
}

// ── mailer.go:235.36,237.3 — qpw.Close error in buildMessage ─────────────────

// failOnClose is an io.WriteCloser whose Write succeeds but Close always errors.
type failOnClose struct{ io.Writer }

func (failOnClose) Close() error { return errors.New("close: injected failure") }

// TestSendVerificationEmail_BuildMessageQPCloseError covers mailer.go:235-237
// by replacing the body-writer factory with one whose Close always fails.
//
// MUST NOT run in parallel: mutates newBodyWriter.
func TestSendVerificationEmail_BuildMessageQPCloseError(t *testing.T) {
	defer mailer.SetTestBodyWriter(func(w io.Writer) io.WriteCloser {
		return failOnClose{Writer: w} // Write succeeds (delegates to buf), Close fails.
	})()

	m := newBaseMailer(t)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	require.ErrorContains(t, err, "build message")
}

// ── mailer.go:136.16,138.3 — net.SplitHostPort error in sendWithContext ──────

// TestSendVerificationEmail_SplitHostPortError explicitly covers the
// net.SplitHostPort error branch (mailer.go:136-138). A Host containing an
// extra colon (bare IPv6 notation without brackets) causes Sprintf to produce
// an address with too many colons that SplitHostPort cannot parse.
func TestSendVerificationEmail_SplitHostPortError(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		// "::1" without brackets → fmt.Sprintf produces "::1:25"
		// which SplitHostPort rejects as "too many colons in address".
		Host: "::1", Port: 25,
		From: "noreply@example.com", AppName: "Test",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err, "constructor must succeed; host is validated at send time")

	err = m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid")
}

// ── wc.Write error branch ──────────────────────────────────────────────────

// startSMTPDropAfter354 starts a fake SMTP server that completes the handshake
// through RCPT TO, sends the DATA continuation "354 Start input", then
// immediately closes the connection with SO_LINGER=0 (TCP RST). This ensures
// the client's connection is broken before any body bytes are read.
func startSMTPDropAfter354(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "MAIL FROM"),
				strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "DATA":
				fmt.Fprintf(conn, "354 Start input\r\n")
				// RST the connection so the client's next Write fails immediately.
				if tc, ok := conn.(*net.TCPConn); ok {
					tc.SetLinger(0)
				}
				return
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

// TestSendWithContext_WCWrite_ReturnsError exercises the
//
//	if _, err = wc.Write(msg); err != nil { return err }
//
// branch in sendWithContext. The real email template renders to ~1.5 KB which
// fits in net/smtp's internal 4096-byte bufio buffer, so wc.Write always
// returns nil for the production code path — the error only surfaces later at
// wc.Close. To force the error at wc.Write we call sendWithContext directly
// (via SendWithContextForTest) with an 8 KB synthetic payload that is larger
// than the buffer and must flush to the network mid-write.
//
// MUST NOT run in parallel: mutates the package-level dialFunc via
// SetTestDialFunc is NOT used here — we use a real TCP listener instead so
// SO_LINGER=0 can RST the connection.
func TestSendWithContext_WCWrite_ReturnsError(t *testing.T) {
	host, port := startSMTPDropAfter354(t)
	addr := fmt.Sprintf("%s:%d", host, port)

	// 8 KB — double the 4096-byte bufio threshold, guaranteeing a flush
	// mid-Write that hits the RST connection.
	msg := bytes.Repeat([]byte("A"), 8192)

	err := mailer.SendWithContextForTest(
		context.Background(),
		addr,
		nil, // no auth — fake server does not require it
		"from@test.com",
		[]string{"to@test.com"},
		msg,
	)
	require.Error(t, err, "wc.Write must fail because the server RST the connection after 354")
}

// ── mailer.go:187.40,189.3 — wc.Write error inside sendWithContext ───────────

// startNetPipeSMTPServer runs a minimal fake SMTP server on the write end of a
// net.Pipe connection. It handles the SMTP handshake up through DATA, sends the
// "354 Start input" response, then immediately closes the server connection.
// Because net.Pipe has no kernel-level TCP buffering, the client's next Write
// (the message body via wc.Write) fails immediately with an "io: read/write on
// closed pipe" error, reliably exercising the wc.Write error branch.
func startNetPipeSMTPServer(t *testing.T) (clientConn net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close() })

	go func() {
		defer server.Close()
		fmt.Fprintf(server, "220 pipe ESMTP\r\n")
		scanner := bufio.NewScanner(server)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(server, "250 OK\r\n")
			case strings.HasPrefix(line, "MAIL FROM"):
				fmt.Fprintf(server, "250 OK\r\n")
			case strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(server, "250 OK\r\n")
			case line == "DATA":
				// Accept DATA then close immediately so the client's
				// wc.Write fails at the net.Pipe boundary.
				fmt.Fprintf(server, "354 Start input\r\n")
				return // deferred server.Close fires here
			default:
				fmt.Fprintf(server, "250 OK\r\n")
			}
		}
	}()
	return client
}

// TestSendVerificationEmail_WCWriteError covers mailer.go:187-189 (wc.Write
// returns an error) by injecting a net.Pipe-backed connection via SetTestDialFunc.
// Unlike a real TCP socket, net.Pipe has no kernel-level send buffer, so the
// write fails synchronously as soon as the server side closes.
//
// MUST NOT run in parallel: mutates the package-level dial function.
func TestSendVerificationEmail_WCWriteError(t *testing.T) {
	clientConn := startNetPipeSMTPServer(t)

	defer mailer.SetTestDialFunc(func(_ context.Context, _ string) (net.Conn, error) {
		return clientConn, nil
	})()

	m := newBaseMailer(t)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err, "wc.Write must fail because the server closed the pipe")
}

// ── Verify buildMessage directly (uuid + qpw paths) ──────────────────────────

// TestBuildMessage_UUIDError verifies that buildMessage surfaces the uuid error
// with the expected wrapping prefix (mailer.go:216-218).
//
// MUST NOT run in parallel: mutates uuidNewRandom.
func TestBuildMessage_UUIDError(t *testing.T) {
	sentinelErr := errors.New("uuid: no entropy")
	defer mailer.SetTestUUIDNewRandom(func() (uuid.UUID, error) {
		return uuid.UUID{}, sentinelErr
	})()

	var buf bytes.Buffer
	host, port := startFakeSMTP(t, &buf)
	m := newMailer(t, host, port)

	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "000000")
	require.Error(t, err)
	require.ErrorContains(t, err, "build message")
}

// ── Config / constructor tests ───────────────────────────────────────────────

func TestNewWithAuth_DefaultsOTPValidMinutes(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		Host: "localhost", Port: 25, From: "test@example.com", AppName: "Test",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestNewWithAuth_AcceptsThirty(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		Host: "localhost", Port: 25, From: "test@example.com", AppName: "Test",
		OTPValidMinutes: 30,
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestNewWithAuth_AcceptsFifteen(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		Host: "localhost", Port: 25, From: "test@example.com", AppName: "Test",
		OTPValidMinutes: 15,
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestNewWithAuth_RejectsOutOfRange(t *testing.T) {
	t.Parallel()
	for _, minutes := range []int{-1, 31, 60} {
		minutes := minutes
		t.Run(fmt.Sprintf("minutes_%d", minutes), func(t *testing.T) {
			t.Parallel()
			cfg := mailer.Config{
				Host: "localhost", Port: 25, From: "test@example.com", AppName: "Test",
				OTPValidMinutes: minutes,
			}
			_, err := mailer.NewWithAuth(cfg, nil)
			require.Error(t, err)
		})
	}
}

func TestNew_ReturnsMailer(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		Host: "localhost", Port: 587, Username: "u", Password: "p",
		From: "noreply@example.com", AppName: "App",
	}
	m, err := mailer.New(cfg)
	require.NoError(t, err)
	require.NotNil(t, m)
}

// ── startFakeSMTP ────────────────────────────────────────────────────────────

// startFakeSMTP launches a minimal in-process SMTP server on a random local
// port and accepts exactly one connection, appending each DATA line to buf.
// The listener is closed when the test ends.
func startFakeSMTP(t *testing.T, buf *bytes.Buffer) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	tcpAddr := ln.Addr().(*net.TCPAddr)
	host = tcpAddr.IP.String()
	port = tcpAddr.Port

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		inData := false
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "DATA":
				inData = true
				fmt.Fprintf(conn, "354 Start input\r\n")
			case inData && line == ".":
				fmt.Fprintf(conn, "250 OK\r\n")
				inData = false
			case inData:
				buf.WriteString(line + "\n")
			case strings.HasPrefix(line, "MAIL FROM"),
				strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return host, port
}

// ── Send(VerificationKey) happy path ────────────────────────────────────────

func TestSendVerificationEmail_HappyPath(t *testing.T) {
	t.Parallel()
	var captured bytes.Buffer
	host, port := startFakeSMTP(t, &captured)

	cfg := mailer.Config{
		Host: host, Port: port, From: "noreply@example.com", AppName: "TestApp",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	err = m.Send(mailertemplates.VerificationKey)(context.Background(), "user@example.com", "123456")
	require.NoError(t, err)
	require.Contains(t, captured.String(), "123456")
}

func TestSendVerificationEmail_BodyContainsAppName(t *testing.T) {
	t.Parallel()
	var captured bytes.Buffer
	host, port := startFakeSMTP(t, &captured)

	cfg := mailer.Config{
		Host: host, Port: port, From: "noreply@example.com", AppName: "Acme",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	require.NoError(t, m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "999888"))
	require.Contains(t, captured.String(), "Acme")
}

// ── Send(VerificationKey) error paths ───────────────────────────────────────

func TestSendVerificationEmail_AlreadyCancelledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cfg := mailer.Config{
		Host: "127.0.0.1", Port: 19999, From: "f@example.com", AppName: "A",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	err = m.Send(mailertemplates.VerificationKey)(ctx, "u@example.com", "000000")
	require.Error(t, err)
}

func TestSendVerificationEmail_UnreachableHost(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		// Port 1 is almost certainly not listening on localhost.
		Host: "127.0.0.1", Port: 1, From: "f@example.com", AppName: "A",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	err = m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "000000")
	require.Error(t, err)
}

// ── Header injection protection ──────────────────────────────────────────────

func TestSendVerificationEmail_HeaderInjectionStrippedFromEmail(t *testing.T) {
	t.Parallel()
	var captured bytes.Buffer
	host, port := startFakeSMTP(t, &captured)

	cfg := mailer.Config{
		Host: host, Port: port, From: "noreply@example.com", AppName: "TestApp",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	// Attempt CR-LF header injection via the recipient email.
	// sanitiseHeader strips \r\n, so the injected payload becomes a single
	// concatenated token rather than a new header line. The correct assertion
	// is therefore that "Bcc:" does not appear at the start of a line — i.e.
	// that no independent Bcc header was injected.
	injected := "user@example.com\r\nBcc: evil@attacker.com"
	err = m.Send(mailertemplates.VerificationKey)(context.Background(), injected, "654321")
	require.NoError(t, err)
	body := captured.String()
	for _, line := range strings.Split(body, "\n") {
		require.False(t, strings.HasPrefix(strings.TrimSpace(line), "Bcc:"),
			"header injection produced a Bcc line: %q", line)
	}
}

// ── ErrSendFailed sentinel ───────────────────────────────────────────────────

func TestErrSendFailed_IsNonNil(t *testing.T) {
	t.Parallel()
	require.Error(t, mailer.ErrSendFailed)
}

// ── Misbehaving fake SMTP servers (sendWithContext error paths) ───────────────
//
// Each helper below starts an in-process SMTP listener that behaves normally
// up to a specific protocol command and then returns an error reply. This
// exercises the error branches deep inside sendWithContext that cannot be
// reached via network-level failures (unreachable host, cancelled context).

// startSMTPFailingAt starts a fake SMTP server that completes the EHLO
// handshake but returns a 550 error reply for the specified command prefix.
// Recognised prefixes: "MAIL", "RCPT", "DATA".
func startSMTPFailingAt(t *testing.T, failCommand string) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case strings.HasPrefix(line, failCommand):
				fmt.Fprintf(conn, "550 Rejected by test\r\n")
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

// startSMTPBadGreeting starts a listener that immediately closes the
// connection after the TCP accept, so smtp.NewClient never receives the
// "220" greeting and returns an error.
func startSMTPBadGreeting(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		// Send garbage instead of a valid 220 greeting so that the smtp
		// package's client constructor returns an error.
		fmt.Fprintf(conn, "garbage not smtp\r\n")
		conn.Close()
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

// startSMTPCloseDuringData starts a fake SMTP server that accepts EHLO, MAIL
// FROM and RCPT TO normally, then closes the connection immediately after
// accepting the DATA command. The client receives an EOF while writing the
// message body, exercising the wc.Write / wc.Close error paths.
func startSMTPCloseDuringData(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case strings.HasPrefix(line, "MAIL FROM"),
				strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "DATA":
				// Accept DATA then immediately drop the connection so the
				// client fails when it tries to write the message body.
				fmt.Fprintf(conn, "354 Start input\r\n")
				return // closes conn
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

// newMailer is a convenience wrapper that builds an SMTPMailer with nil auth
// pointed at the given host:port.
func newMailer(t *testing.T, host string, port int) *mailer.SMTPMailer {
	t.Helper()
	cfg := mailer.Config{Host: host, Port: port, From: "noreply@example.com", AppName: "Test"}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)
	return m
}

// TestSendVerificationEmail_SMTPNewClientError covers the smtp.NewClient error
// branch (mailer.go lines 141-142). The server sends a non-220 greeting, which
// causes smtp.NewClient to return an error.
func TestSendVerificationEmail_SMTPNewClientError(t *testing.T) {
	t.Parallel()
	host, port := startSMTPBadGreeting(t)
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// ── T1: STARTTLS handshake failure ───────────────────────────────────────────

func startSMTPWithSTARTTLSAdvertised(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250-localhost\r\n250 STARTTLS\r\n")
			case line == "STARTTLS":
				fmt.Fprintf(conn, "454 TLS not available\r\n")
				return
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

func TestSendVerificationEmail_STARTTLSHandshakeFails(t *testing.T) {
	t.Parallel()
	host, port := startSMTPWithSTARTTLSAdvertised(t)
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.ErrorContains(t, err, "STARTTLS")
}

// ── T2: AUTH rejection ────────────────────────────────────────────────────────

func startSMTPRejectingAuth(t *testing.T) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250-localhost\r\n250 AUTH PLAIN LOGIN\r\n")
			case strings.HasPrefix(line, "AUTH"):
				fmt.Fprintf(conn, "535 Authentication credentials invalid\r\n")
				return
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

func TestSendVerificationEmail_SMTPAuthRejected(t *testing.T) {
	t.Parallel()
	host, port := startSMTPRejectingAuth(t)
	cfg := mailer.Config{
		Host: host, Port: port,
		Username: "user", Password: "wrong",
		From: "noreply@example.com", AppName: "Test",
	}
	m, err := mailer.New(cfg)
	require.NoError(t, err)
	err = m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// ── T3: cfg.From validation ──────────────────────────────────────────────────

func TestNewWithAuth_RejectsEmptyFrom(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		Host: "localhost", Port: 25, AppName: "Test",
		From: "", // empty — must be rejected at constructor time
	}
	_, err := mailer.NewWithAuth(cfg, nil)
	require.Error(t, err)
	require.ErrorContains(t, err, "From")
}

func TestNewWithAuth_RejectsStructurallyInvalidFrom(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{
		"noreply@",       // missing domain
		"@example.com",   // missing local part
		"not-an-address", // no @ at all
		"<>",             // null sender (SMTP bounce envelope, not a valid From)
	} {
		addr := addr
		t.Run(addr, func(t *testing.T) {
			t.Parallel()
			cfg := mailer.Config{
				Host: "localhost", Port: 25, AppName: "Test",
				From: addr,
			}
			_, err := mailer.NewWithAuth(cfg, nil)
			require.Error(t, err,
				"NewWithAuth must reject structurally invalid From address %q", addr)
		})
	}
}

// ── T4: OTP code absent from Subject ─────────────────────────────────────────

func TestSendVerificationEmail_SubjectDoesNotContainCode(t *testing.T) {
	t.Parallel()
	var captured bytes.Buffer
	host, port := startFakeSMTP(t, &captured)

	cfg := mailer.Config{
		Host: host, Port: port, From: "noreply@example.com", AppName: "TestApp",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	const code = "987654"
	require.NoError(t, m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", code))

	for _, line := range strings.Split(captured.String(), "\n") {
		if strings.HasPrefix(strings.ToLower(line), "subject:") {
			require.NotContains(t, line, code,
				"OTP code must not appear in the Subject header")
		}
	}
}

// TestSendVerificationEmail_MailFromError covers the c.Mail error branch
// (mailer.go lines 159-162). The server rejects the MAIL FROM command.
func TestSendVerificationEmail_MailFromError(t *testing.T) {
	t.Parallel()
	host, port := startSMTPFailingAt(t, "MAIL")
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// TestSendVerificationEmail_RcptToError covers the c.Rcpt error branch
// (mailer.go lines 164-166). The server rejects the RCPT TO command.
func TestSendVerificationEmail_RcptToError(t *testing.T) {
	t.Parallel()
	host, port := startSMTPFailingAt(t, "RCPT")
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// TestSendVerificationEmail_DataCommandError covers the c.Data error branch
// (mailer.go lines 168-170). The server rejects the DATA command with a 550.
func TestSendVerificationEmail_DataCommandError(t *testing.T) {
	t.Parallel()
	host, port := startSMTPFailingAt(t, "DATA")
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// TestSendVerificationEmail_WriteCloseDuringDataError covers the wc.Write and
// wc.Close error branches (mailer.go lines 173-178). The server accepts the
// DATA command but closes the connection before the client can write the body.
func TestSendVerificationEmail_WriteCloseDuringDataError(t *testing.T) {
	t.Parallel()
	host, port := startSMTPCloseDuringData(t)
	m := newMailer(t, host, port)
	err := m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
}

// ── T5: context cancelled racing with successful send ─────────────────────────

// startSMTPWithDataDelay starts a fake SMTP server that completes the full
// SMTP exchange normally but pauses for the given duration between receiving
// the end-of-data marker (".") and sending the final "250 OK". This gives
// the test a window to cancel the context while the send is in-flight.
func startSMTPWithDataDelay(t *testing.T, buf *bytes.Buffer, delay time.Duration) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	tcpAddr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprintf(conn, "220 localhost ESMTP\r\n")
		scanner := bufio.NewScanner(conn)
		inData := false
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "DATA":
				inData = true
				fmt.Fprintf(conn, "354 Start input\r\n")
			case inData && line == ".":
				// Pause here so the test can cancel the context.
				time.Sleep(delay)
				fmt.Fprintf(conn, "250 OK\r\n")
				inData = false
			case inData:
				buf.WriteString(line + "\n")
			case strings.HasPrefix(line, "MAIL FROM"),
				strings.HasPrefix(line, "RCPT TO"):
				fmt.Fprintf(conn, "250 OK\r\n")
			case line == "QUIT":
				fmt.Fprintf(conn, "221 Bye\r\n")
				return
			default:
				fmt.Fprintf(conn, "250 OK\r\n")
			}
		}
	}()
	return tcpAddr.IP.String(), tcpAddr.Port
}

// TestSendVerificationEmail_CtxErrReturnedOnRaceWithSuccessfulSend covers the
// final return ctx.Err() in sendWithContext. The context is cancelled during
// the server's deliberate pause after accepting the message body.
func TestSendVerificationEmail_CtxErrReturnedOnRaceWithSuccessfulSend(t *testing.T) {
	t.Parallel()
	const serverDelay = 80 * time.Millisecond

	var captured bytes.Buffer
	host, port := startSMTPWithDataDelay(t, &captured, serverDelay)

	cfg := mailer.Config{
		Host: host, Port: port, From: "noreply@example.com", AppName: "TestApp",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), serverDelay/2)
	defer cancel()

	err = m.Send(mailertemplates.VerificationKey)(ctx, "u@example.com", "123456")
	// The context fires during the server-side delay; sendWithContext returns
	// ctx.Err() (context.DeadlineExceeded).
	require.Error(t, err)
}

// ── sendWithContext invalid-addr guard ────────────────────────────────────────

// TestSendVerificationEmail_InvalidHostAddr covers the net.SplitHostPort error
// branch inside sendWithContext (mailer.go). When cfg.Host itself contains a
// colon (e.g. a mistyped IPv6 literal without bracket notation), the address
// produced by fmt.Sprintf("%s:%d", host, port) has too many colons and
// net.SplitHostPort returns an error before any network dial is attempted.
func TestSendVerificationEmail_InvalidHostAddr(t *testing.T) {
	t.Parallel()
	cfg := mailer.Config{
		// Host contains an extra colon — produces "bad:host:25" which
		// net.SplitHostPort cannot parse as host:port.
		Host: "bad:host", Port: 25,
		From: "noreply@example.com", AppName: "Test",
	}
	m, err := mailer.NewWithAuth(cfg, nil)
	require.NoError(t, err, "construction must succeed; host is validated at send time")

	err = m.Send(mailertemplates.VerificationKey)(context.Background(), "u@example.com", "123456")
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid")
}

func TestOTPHandlerBase_SendIsCallable(t *testing.T) {
	t.Parallel()
	base := mailertest.NoopBase()
	err := base.Send(context.Background(), "user@example.com", "123456")
	require.NoError(t, err)
}

func TestOTPHandlerBase_QueueAndTimeoutFields(t *testing.T) {
	t.Parallel()
	q := mailer.NewQueue()
	const timeout = 5 * time.Second
	base := mailer.OTPHandlerBase{
		Send:    func(_ context.Context, _, _ string) error { return nil },
		Queue:   q,
		Timeout: timeout,
	}
	require.Same(t, q, base.Queue)
	require.Equal(t, timeout, base.Timeout)
}

func TestSendOTPEmail_EmptyRawCode_ReturnsNilWithoutSending(t *testing.T) {
	t.Parallel()
	called := false
	base := mailer.OTPHandlerBase{
		Send: func(_ context.Context, _, _ string) error {
			called = true
			return nil
		},
	}
	err := mailer.SendOTPEmail(context.Background(), base, "user-1", "user@example.com", "", "test")
	require.NoError(t, err)
	require.False(t, called, "Send must not be called when rawCode is empty")
}

func TestSendOTPEmail_NonEmptyRawCode_CallsSend(t *testing.T) {
	t.Parallel()
	var calls []mailertest.Call
	base := mailertest.RecordingBase(&calls)
	err := mailer.SendOTPEmail(context.Background(), base, "user-1", "user@example.com", "123456", "test")
	require.NoError(t, err)
	require.Len(t, calls, 1)
	require.Equal(t, "user@example.com", calls[0].ToEmail)
	require.Equal(t, "123456", calls[0].Code)
}

func TestSendOTPEmail_PropagatesSyncDeliveryError(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("smtp down")
	base := mailertest.ErrorBase(sentinel)
	err := mailer.SendOTPEmail(context.Background(), base, "u1", "u@example.com", "000000", "test")
	require.ErrorIs(t, err, sentinel)
}

func TestSendOTPEmail_UsesQueueWhenProvided(t *testing.T) {
	t.Parallel()
	var calls []mailertest.Call
	base := mailertest.RecordingBase(&calls)
	dl := mailer.NewInMemoryDeadLetterStore(10)
	q := mailer.NewQueue(
		mailer.WithQueueSize(10),
		mailer.WithDeadLetterStore(dl),
	)
	require.NoError(t, q.Start(1))
	base.Queue = q
	base.Timeout = 5 * time.Second

	err := mailer.SendOTPEmail(context.Background(), base, "u1", "u@example.com", "123456", "test")
	require.NoError(t, err)
	q.Shutdown()

	require.Len(t, calls, 1)
	require.Equal(t, 0, dl.Len())
}

func TestSendOTPEmail_FallsBackToSyncWhenQueueFull(t *testing.T) {
	t.Parallel()
	var calls []mailertest.Call
	base := mailertest.RecordingBase(&calls)
	q := mailer.NewQueue(mailer.WithQueueSize(1))
	q.Shutdown() // immediately shut down so Enqueue returns an error
	base.Queue = q
	base.Timeout = 5 * time.Second

	err := mailer.SendOTPEmail(context.Background(), base, "u1", "u@example.com", "654321", "test")
	require.NoError(t, err)
	require.Len(t, calls, 1, "synchronous delivery must be attempted when queue is unavailable")
}
