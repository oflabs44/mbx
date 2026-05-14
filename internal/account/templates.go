package account

import "strings"

// Templates are emitted by `mbx account add`. Each is structurally a
// commented worked example from docs/config.md, with REPLACE markers for
// the values the user must fill in. The `{{name}}` token is substituted at
// emit time so the rendered block is immediately copy-pasteable into the
// `account auth` / `account doctor` flow.

const gmailTemplate = `# Fill in the REPLACE values, then run ` + "`mbx account auth {{name}}`" + `.
[accounts.{{name}}]
type  = "gmail"
email = "you@gmail.com"

[accounts.{{name}}.backend.auth]
type      = "oauth2"
client-id = "REPLACE.apps.googleusercontent.com"
auth-url  = "https://accounts.google.com/o/oauth2/v2/auth"
token-url = "https://www.googleapis.com/oauth2/v3/token"
# redirect-host = "localhost"
# redirect-port = 0

[accounts.{{name}}.backend.auth.client-secret]
cmd = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/client-secret"

[accounts.{{name}}.backend.auth.refresh-token]
cmd       = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/refresh-token"
write_cmd = "REPLACE  # e.g. op item edit mbx-{{name}} refresh-token[password]=-"
`

const imapTemplate = `# Fill in the REPLACE values, then run ` + "`mbx account doctor {{name}}`" + `.
[accounts.{{name}}]
type  = "imap"
email = "you@example.com"

[accounts.{{name}}.backend]
host = "imap.example.com"
port = 993
tls  = "tls"

[accounts.{{name}}.backend.auth]
type     = "password"
username = "you@example.com"
cmd      = "REPLACE  # e.g. op read op://Dev/mbx-{{name}}/password"

[accounts.{{name}}.send]
host = "smtp.example.com"
port = 587
tls  = "starttls"
# send.auth inherits from backend.auth unless an explicit block is added.
`

// GmailTemplate returns the gmail account skeleton with {{name}} substituted.
func GmailTemplate(name string) string {
	return strings.ReplaceAll(gmailTemplate, "{{name}}", name)
}

// IMAPTemplate returns the IMAP account skeleton with {{name}} substituted.
func IMAPTemplate(name string) string {
	return strings.ReplaceAll(imapTemplate, "{{name}}", name)
}
