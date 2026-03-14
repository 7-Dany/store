package templates

// OwnerTransferKey is the canonical identifier for the ownership-transfer token email.
const OwnerTransferKey = "owner_transfer"

// OwnerTransferEmailTemplate is the HTML template for ownership-transfer invitation
// emails. The {{.Code}} field carries the full raw transfer token. Exported as a
// pointer so tests can substitute an invalid template string to exercise error paths
// in mailer.NewWithAuth without touching the mailer infrastructure.
var OwnerTransferEmailTemplate = &ownerTransferEmailTemplateStr

var ownerTransferEmailTemplateStr = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width,initial-scale=1.0">
  <title>Ownership transfer invitation</title>
</head>
<body style="margin:0;padding:0;background:#f1f3f4;font-family:'Google Sans',Roboto,Arial,sans-serif;">
  <table width="100%" cellpadding="0" cellspacing="0" style="background:#f1f3f4;padding:48px 0;">
    <tr><td align="center">
      <table width="520" cellpadding="0" cellspacing="0"
             style="background:#ffffff;border-radius:8px;overflow:hidden;box-shadow:0 1px 3px rgba(60,64,67,.15),0 4px 8px rgba(60,64,67,.1);">

        <!-- orange top accent — distinct from the blue verification email -->
        <tr><td style="height:4px;background:#e8710a;font-size:0;line-height:0;">&nbsp;</td></tr>

        <!-- header -->
        <tr><td style="padding:32px 48px 0;">
          <p style="margin:0;font-size:22px;font-weight:700;color:#e8710a;letter-spacing:-0.5px;">{{.AppName}}</p>
        </td></tr>

        <!-- body -->
        <tr><td style="padding:24px 48px 0;">
          <p style="margin:0 0 6px;font-size:20px;font-weight:600;color:#202124;">You have been invited to become the owner</p>
          <p style="margin:0;font-size:14px;color:#5f6368;line-height:1.6;">
            The current owner of <strong style="color:#202124;">{{.AppName}}</strong> has initiated an ownership
            transfer to your account. Use the secure token below to accept. This token
            expires in <strong style="color:#202124;">48 hours</strong> and can only be used once.
          </p>
        </td></tr>

        <!-- transfer token -->
        <tr><td style="padding:28px 48px;">
          <table cellpadding="0" cellspacing="0" width="100%">
            <tr>
              <td style="background:#fff3e0;border-radius:8px;padding:18px 24px;word-break:break-all;">
                <p style="margin:0 0 6px;font-size:11px;font-weight:600;color:#e8710a;letter-spacing:0.5px;text-transform:uppercase;">Transfer token</p>
                <span style="font-size:13px;font-weight:600;letter-spacing:1px;color:#202124;font-family:monospace;">{{.Code}}</span>
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
                  If you did not expect this invitation, do not use this token and contact
                  the current owner immediately. Never share this token with anyone &mdash;
                  {{.AppName}} will never ask for it.
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
