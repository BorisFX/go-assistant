package migrations

import "embed"

// FS embeds all SQL schema migrations into the assistant binary so deploys do
// not depend on the working directory or separate file copies.
//
//go:embed *.sql
var FS embed.FS
