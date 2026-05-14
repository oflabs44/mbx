package account

import "strings"

// Templates are emitted by `mbx account add`. Each is a one-block dotted-key
// scaffold per ADR-0006: section header `[accounts.<name>]` followed by all
// settings as dotted keys. The `{{name}}` token is substituted at emit time
// so the rendered block is immediately a starting point for `account auth` /
// `account doctor`.

const gmailTemplate = `# Fill in the REPLACE values, then run ` + "`mbx account auth {{name}}`" + `.
[accounts.{{name}}]
email = "you@gmail.com"

backend.type  = "gmail"
backend.login = "you@gmail.com"

backend.auth.type      = "oauth2"
backend.auth.client-id = "REPLACE.apps.googleusercontent.com"
backend.auth.auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
backend.auth.token-url = "https://www.googleapis.com/oauth2/v3/token"
backend.auth.method    = "xoauth2"
backend.auth.scopes    = ["https://mail.google.com/"]
# backend.auth.pkce          = true
# backend.auth.redirect-host = "localhost"
# backend.auth.redirect-port = 0

backend.auth.client-secret.cmd = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/client-secret"

backend.auth.refresh-token.cmd       = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/refresh-token"
backend.auth.refresh-token.write_cmd = "REPLACE  # e.g. op item edit mbx-{{name}} refresh-token[password]=-"
`

const imapTemplate = `# Fill in the REPLACE values, then run ` + "`mbx account doctor {{name}}`" + `.
[accounts.{{name}}]
email = "you@example.com"

backend.type            = "imap"
backend.host            = "imap.example.com"
backend.port            = 993
backend.encryption.type = "tls"
backend.login           = "you@example.com"
backend.auth.type       = "password"
backend.auth.cmd        = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/imap-password"

message.send.backend.type            = "smtp"
message.send.backend.host            = "smtp.example.com"
message.send.backend.port            = 587
message.send.backend.encryption.type = "start-tls"
message.send.backend.login           = "you@example.com"
message.send.backend.auth.type       = "password"
message.send.backend.auth.cmd        = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/smtp-password"

folder.aliases.inbox  = "INBOX"
folder.aliases.sent   = "Sent"
folder.aliases.drafts = "Drafts"
folder.aliases.trash  = "Trash"
`

// GmailTemplate returns the gmail account skeleton with {{name}} substituted.
func GmailTemplate(name string) string {
	return strings.ReplaceAll(gmailTemplate, "{{name}}", name)
}

// IMAPTemplate returns the IMAP account skeleton with {{name}} substituted.
func IMAPTemplate(name string) string {
	return strings.ReplaceAll(imapTemplate, "{{name}}", name)
}
