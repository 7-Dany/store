// Package templates holds the HTML email templates for every transactional
// email type. Each file owns one template string and its exported pointer so
// tests can swap it out without touching the mailer infrastructure.
package templates

// VerificationKey is the canonical identifier for the email-verification OTP template.
const VerificationKey = "verification"

// VerificationEmailTemplate is the HTML template for email-verification OTP
// emails. Exported as a pointer so tests can substitute an invalid template
// string to exercise the template.Parse error branch in mailer.NewWithAuth.
var VerificationEmailTemplate = &verificationEmailTemplateStr

var verificationEmailTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Verify your email</title>
</head>
<body style="margin:0;padding:0;background:#f1f3f4;font-family:'Google Sans',Roboto,Arial,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background:#f1f3f4;padding:48px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0"
             style="background:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(60,64,67,.15),0 4px 8px rgba(60,64,67,.1);">

        <!-- blue top accent -->
        <tr><td style="height:4px;background:#1a73e8;font-size:0;line-height:0;">&nbsp;</td></tr>

        <!-- header -->
        <tr><td style="padding:32px 48px 0;">
          <p style="margin:0;font-size:22px;font-weight:700;color:#1a73e8;letter-spacing:-0.5px;">{{.AppName}}</p>
        </td></tr>

        <!-- body -->
        <tr><td style="padding:24px 48px 0;">
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">Verify your email address</p>
          <p style="margin:0;font-size:14px;color:#5f6368;line-height:1.6;">
            Use the verification code below to confirm your email address.
            This code expires in <strong style="color:#202124;">{{.ValidMins}} minutes</strong>.
          </p>
        </td></tr>

        <!-- OTP code -->
        <tr><td align="center" style="padding:28px 48px;">
          <table cellpadding="0" cellspacing="0">
            <tr>
              <td style="background:#e8f0fe;border-radius:8px;padding:18px 36px;text-align:center;">
                <span style="font-size:34px;font-weight:700;letter-spacing:12px;color:#1a73e8;font-family:monospace;">{{.Code}}</span>
              </td>
            </tr>
          </table>
        </td></tr>

        <!-- security notice -->
        <tr><td style="padding:0 48px 32px;">
          <table cellpadding="0" cellspacing="0" width="100%">
            <tr>
              <td style="background:#fafafa;border-left:3px solid #dadce0;border-radius:0 4px 4px 0;padding:12px 16px;">
                <p style="margin:0;font-size:12px;color:#5f6368;line-height:1.6;">
                  If you didn&rsquo;t create a {{.AppName}} account, you can safely ignore this email.
                  Never share this code with anyone &mdash; {{.AppName}} will never ask for it.
                </p>
              </td>
            </tr>
          </table>
        </td></tr>

        <!-- footer -->
        <tr><td style="padding:20px 48px;border-top:1px solid #e8eaed;">
          <p style="margin:0;font-size:12px;color:#80868b;text-align:center;">
            &copy; {{.Year}} {{.AppName}} &nbsp;&middot;&nbsp; This is an automated message, please do not reply.
          </p>
        </td></tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`
