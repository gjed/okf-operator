// Package prompts holds the model prompts `write` sends. They are embedded at
// build time, so a given binary always sends the same instructions.
package prompts

import _ "embed"

// System is the system message for every page call.
//
//go:embed system.md
var System string

// Page is the text/template for the user message of one page call.
//
//go:embed page.md
var Page string
