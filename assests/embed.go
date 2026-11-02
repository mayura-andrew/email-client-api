// Package assests embeds the default certificate background, used until an
// admin uploads a template in Settings.
package assests

import _ "embed"

//go:embed template.jpg
var Template []byte
