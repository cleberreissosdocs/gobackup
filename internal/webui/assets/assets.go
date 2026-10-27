// Package assets embeds the GoBackup shield logo so both the agent and
// central web UIs can serve it as a normal, browser-cacheable static file
// (GET /logo.png, GET /favicon.ico) instead of inflating every rendered
// page with a repeated base64 blob.
package assets

import _ "embed"

//go:embed logo.png
var LogoPNG []byte

//go:embed favicon.ico
var FaviconICO []byte
