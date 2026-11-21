// Package prompts holds the model prompts `write` and `review` send. They are embedded at
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

// CreateSystem is the system message for every page creation call.
//
//go:embed create-system.md
var CreateSystem string

// Create is the text/template for the user message of one page creation call.
//
//go:embed create.md
var Create string

// ReviewSystem is the system message for every review call.
//
//go:embed review-system.md
var ReviewSystem string

// Review is the text/template for the user message of one review call.
//
//go:embed review.md
var Review string
