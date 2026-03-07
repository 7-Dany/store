package templates

// EmailChangeOTPKey is the canonical identifier for the email-change OTP template.
// This email is sent to the user's current address to verify they control it before
// a change is applied.
const EmailChangeOTPKey = "email_change_otp"

// EmailChangeConfirmOTPKey is the canonical identifier for the email-change
// confirmation OTP template. This email is sent to the new address to verify
// the user controls it before the change is committed.
const EmailChangeConfirmOTPKey = "email_change_confirm_otp"

// EmailChangedNotificationKey is the canonical identifier for the post-change
// notification template. This email is sent to the old address after the change
// has been successfully committed, so the previous owner can detect abuse.
// The template data Code field is unused; pass "" when calling Send.
const EmailChangedNotificationKey = "email_changed_notification"

// EmailChangeOTPTemplate is the HTML template for the OTP sent to the user's
// current email address during step 1 of the email-change flow.
var EmailChangeOTPTemplate = &emailChangeOTPTemplateStr

var emailChangeOTPTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Your email change request</title>
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
            We received a request to change the email address on your {{.AppName}} account.
            Enter the code below to confirm it&#39;s really you.
            It expires in <strong>{{.ValidMins}} minutes</strong>.
          </p>
        </td></tr>
        <tr><td align="center" style="padding-bottom:28px;">
          <div style="display:inline-block;background:#f4f4f5;border-radius:8px;padding:20px 40px;">
            <span style="font-size:36px;font-weight:700;letter-spacing:10px;color:#18181b;font-family:monospace;">
              {{.Code}}
            </span>
          </div>
        </td></tr>
        <tr><td style="color:#71717a;font-size:13px;line-height:1.5;">
          <p style="margin:0 0 8px;">
            If you did not request this change, you can safely ignore this email.
            Your account will not be affected.
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

// EmailChangeConfirmOTPTemplate is the HTML template for the OTP sent to the
// user's new email address during step 2 of the email-change flow.
var EmailChangeConfirmOTPTemplate = &emailChangeConfirmOTPTemplateStr

var emailChangeConfirmOTPTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Confirm your new email</title>
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
            Use the code below to confirm this as your new email address.
            It expires in <strong>{{.ValidMins}} minutes</strong>.
          </p>
        </td></tr>
        <tr><td align="center" style="padding-bottom:28px;">
          <div style="display:inline-block;background:#f4f4f5;border-radius:8px;padding:20px 40px;">
            <span style="font-size:36px;font-weight:700;letter-spacing:10px;color:#18181b;font-family:monospace;">
              {{.Code}}
            </span>
          </div>
        </td></tr>
        <tr><td style="color:#71717a;font-size:13px;line-height:1.5;">
          <p style="margin:0 0 8px;">
            If you did not request this change, you can safely ignore this email.
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

// EmailChangedNotificationTemplate is the HTML template for the notification
// sent to the user's old email address after a successful email change.
// The Code field is not rendered; pass "" when calling Send.
var EmailChangedNotificationTemplate = &emailChangedNotificationTemplateStr

var emailChangedNotificationTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Your email address has been changed</title>
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
            The email address associated with your {{.AppName}} account has been
            successfully changed. This address will no longer receive account
            notifications.
          </p>
          <p style="margin:0 0 24px;">
            If you did not make this change, please contact our support team
            immediately to secure your account.
          </p>
        </td></tr>
        <tr><td style="color:#71717a;font-size:13px;line-height:1.5;">
          <p style="margin:0;">
            You are receiving this message because this address was previously
            registered on your {{.AppName}} account.
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
