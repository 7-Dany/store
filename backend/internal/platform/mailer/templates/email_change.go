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
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">Confirm your email change</p>
          <p style="margin:0;font-size:14px;color:#5f6368;line-height:1.6;">
            We received a request to change the email address on your {{.AppName}} account.
            Enter the code below to confirm it&#39;s really you.
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
                  If you did not request this change, you can safely ignore this email &mdash;
                  your account will not be affected. Never share this code with anyone.
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
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">Confirm your new email address</p>
          <p style="margin:0;font-size:14px;color:#5f6368;line-height:1.6;">
            Use the code below to confirm this as your new email address.
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
                  If you did not request this change, you can safely ignore this email &mdash;
                  your current email will remain unchanged. Never share this code with anyone.
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
        <tr><td style="padding:24px 48px 32px;">
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">Your email address was changed</p>
          <p style="margin:0 0 16px;font-size:14px;color:#5f6368;line-height:1.6;">
            The email address on your {{.AppName}} account has been successfully updated.
            This address will no longer receive account notifications.
          </p>

          <!-- alert box -->
          <table cellpadding="0" cellspacing="0" width="100%">
            <tr>
              <td style="background:#fef7e0;border-left:3px solid #f9ab00;border-radius:0 4px 4px 0;padding:12px 16px;">
                <p style="margin:0;font-size:12px;color:#5f6368;line-height:1.6;">
                  <strong style="color:#202124;">Wasn&rsquo;t you?</strong> If you did not make this change, please
                  contact support immediately to secure your account.
                </p>
              </td>
            </tr>
          </table>
        </td></tr>

        <!-- footer -->
        <tr><td style="padding:20px 48px;border-top:1px solid #e8eaed;">
          <p style="margin:0;font-size:12px;color:#80868b;text-align:center;">
            &copy; {{.Year}} {{.AppName}} &nbsp;&middot;&nbsp; You received this because this address was previously registered on your account.
          </p>
        </td></tr>

      </table>
    </td></tr>
  </table>
</body>
</html>`
