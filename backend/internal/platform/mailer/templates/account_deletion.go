package templates

// AccountDeletionOTPKey is the canonical identifier for the account-deletion
// OTP email template.
const AccountDeletionOTPKey = "account_deletion_otp"

// AccountDeletionOTPTemplate is the HTML template for account-deletion OTP emails.
// Subject "Delete your {AppName} account" is intentionally distinct from all
// other transactional email subjects so e2e tests can query Gmail without
// matching unrelated messages.
// Exported as a pointer so tests can substitute an invalid template string.
var AccountDeletionOTPTemplate = &accountDeletionOTPTemplateStr

var accountDeletionOTPTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Delete your account</title>
</head>
<body style="margin:0;padding:0;font-family:Arial,Helvetica,sans-serif;background:#f4f4f5;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background:#f4f4f5;padding:40px 0;">
    <tr><td align="center">
      <table width="480" cellpadding="0" cellspacing="0"
             style="background:#fff;border-radius:8px;padding:40px;box-shadow:0 2px 8px rgba(0,0,0,.08);">
        <tr><td style="text-align:center;padding-bottom:24px;">
          <h1 style="margin:0;font-size:22px;color:#18181b;">{{.AppName}}</h1>
        </td></tr>
        <tr><td style="color:#3f3f46;font-size:15px;line-height:1.6;">
          <p style="margin:0 0 16px;">Hello,</p>
          <p style="margin:0 0 24px;">
            You requested to delete your <strong>{{.AppName}}</strong> account.
            Use the confirmation code below to complete the request.
            It expires in <strong>{{.ValidMins}} minutes</strong>.
          </p>
        </td></tr>
        <tr><td align="center" style="padding-bottom:28px;">
          <div style="display:inline-block;background:#f4f4f5;border-radius:8px;padding:20px 40px;">
            <span data-otp="{{.Code}}" style="font-size:36px;font-weight:700;letter-spacing:10px;color:#18181b;font-family:monospace;">
              {{.Code}}
            </span>
          </div>
        </td></tr>
        <tr><td style="color:#71717a;font-size:13px;line-height:1.5;">
          <p style="margin:0 0 8px;">
            If you did not request account deletion, you can safely ignore this email.
            Your account will not be deleted.
          </p>
          <p style="margin:0;">
            Never share this code with anyone. {{.AppName}} will never ask for it by phone or chat.
          </p>
        </td></tr>
        <tr><td style="padding-top:32px;border-top:1px solid #e4e4e7;color:#a1a1aa;font-size:12px;text-align:center;">
          &copy; {{.Year}} {{.AppName}}. All rights reserved.
        </td></tr>
      </table>
    </td></tr>
  </table>
</body>
</html>`
