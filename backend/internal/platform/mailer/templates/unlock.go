package templates

// UnlockKey is the canonical identifier for the account-unlock OTP template.
const UnlockKey = "unlock"

// UnlockEmailTemplate is the HTML template for account-unlock OTP emails.
// Subject "Unlock your {AppName} account" is intentionally distinct from the
// verification subject so e2e tests can query Gmail by subject without
// matching the wrong message.
// Exported as a pointer so tests can substitute an invalid template string.
var UnlockEmailTemplate = &unlockEmailTemplateStr

var unlockEmailTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Unlock your account</title>
</head>
<body style="margin:0;padding:0;background:#f1f3f4;font-family:'Google Sans',Roboto,Arial,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background:#f1f3f4;padding:48px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0"
             style="background:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(60,64,67,.15),0 4px 8px rgba(60,64,67,.1);">

        <!-- amber top accent for security alert -->
        <tr><td style="height:4px;background:#f9ab00;font-size:0;line-height:0;">&nbsp;</td></tr>

        <!-- header -->
        <tr><td style="padding:32px 48px 0;">
          <p style="margin:0;font-size:22px;font-weight:700;color:#1a73e8;letter-spacing:-0.5px;">{{.AppName}}</p>
        </td></tr>

        <!-- body -->
        <tr><td style="padding:24px 48px 0;">
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">Unlock your account</p>
          <p style="margin:0;font-size:14px;color:#5f6368;line-height:1.6;">
            Your account was locked after too many failed sign-in attempts.
            Use the code below to unlock it. This code expires in <strong style="color:#202124;">{{.ValidMins}} minutes</strong>.
          </p>
        </td></tr>

        <!-- OTP code -->
        <tr><td align="center" style="padding:28px 48px;">
          <table cellpadding="0" cellspacing="0">
            <tr>
              <td style="background:#fef7e0;border-radius:8px;padding:18px 36px;text-align:center;">
                <span style="font-size:34px;font-weight:700;letter-spacing:12px;color:#e37400;font-family:monospace;">{{.Code}}</span>
              </td>
            </tr>
          </table>
        </td></tr>

        <!-- security notice -->
        <tr><td style="padding:0 48px 32px;">
          <table cellpadding="0" cellspacing="0" width="100%">
            <tr>
              <td style="background:#fafafa;border-left:3px solid #f9ab00;border-radius:0 4px 4px 0;padding:12px 16px;">
                <p style="margin:0;font-size:12px;color:#5f6368;line-height:1.6;">
                  If you didn&rsquo;t request this, someone may be trying to access your account.
                  You can ignore this email &mdash; your account will stay locked.
                  Never share this code with anyone.
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
