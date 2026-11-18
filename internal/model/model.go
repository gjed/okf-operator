// Package model calls one OpenAI-compatible chat completions endpoint. It
// sends text and returns text: no tools, no function calls, no retries, and no
// credentials other than the model API key.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	requestTimeout = 5 * time.Minute
	// maxErrorBody bounds how much of an error reply is read and kept.
	maxErrorBody = 512
	// maxReplyBody bounds a successful reply so a misbehaving endpoint cannot
	// exhaust memory.
	maxReplyBody = 16 << 20
	redacted     = "[REDACTED]"
)

var defaultHTTP = &http.Client{Timeout: requestTimeout}

// Client calls a chat completions endpoint.
type Client struct {
	BaseURL string              // e.g. http://localhost:11434/v1; requests go to BaseURL + "/chat/completions"
	Model   string              // model name sent with each request
	APIKey  string              // optional; when empty no Authorization header is sent
	HTTP    *http.Client        // nil → 5-minute timeout client
	Redact  func(string) string // optional; applied to error messages; nil → identity
}

// Request is one system and user message pair with an output token budget.
type Request struct {
	System, User string
	MaxTokens    int
}

// Usage is the token accounting the endpoint reported.
type Usage struct {
	Prompt, Completion int
	Reported           bool // false when the endpoint omitted usage
}

// Reply is the model's text with its finish reason and usage.
type Reply struct {
	Text         string
	FinishReason string
	Usage        Usage
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string    `json:"model"`
	Messages    []message `json:"messages"`
	Temperature int       `json:"temperature"`
	MaxTokens   int       `json:"max_tokens"`
	Stream      bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message      message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Complete sends r and returns the first choice. An HTTP status of 400 or
// above, a transport error, or a reply with no choices is an error. Error
// messages never contain APIKey.
func (c *Client) Complete(ctx context.Context, r Request) (Reply, error) {
	body, err := json.Marshal(chatRequest{
		Model: c.Model,
		Messages: []message{
			{Role: "system", Content: r.System},
			{Role: "user", Content: r.User},
		},
		Temperature: 0,
		MaxTokens:   r.MaxTokens,
		Stream:      false,
	})
	if err != nil {
		return Reply{}, c.fail("encode request", err)
	}

	url := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Reply{}, c.fail("build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	hc := c.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Reply{}, c.fail("model request", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		// Redact the full bounded body before truncating, so a key that
		// straddles the 512-byte cut is replaced rather than split.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxReplyBody))
		text := c.redact(string(raw))
		if len(text) > maxErrorBody {
			text = text[:maxErrorBody]
		}
		return Reply{}, c.fail(fmt.Sprintf("model endpoint returned HTTP %d", resp.StatusCode), text)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBody))
	if err != nil {
		return Reply{}, c.fail("read model reply", err)
	}
	var out chatResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return Reply{}, c.fail("decode model reply", err)
	}
	if len(out.Choices) == 0 {
		return Reply{}, errors.New("model reply has no choices")
	}

	reply := Reply{
		Text:         out.Choices[0].Message.Content,
		FinishReason: out.Choices[0].FinishReason,
	}
	if out.Usage != nil {
		reply.Usage = Usage{
			Prompt:     out.Usage.PromptTokens,
			Completion: out.Usage.CompletionTokens,
			Reported:   true,
		}
	}
	return reply, nil
}

// fail returns an error with what and detail, redacted. The cause is folded
// into the text rather than wrapped, so Unwrap cannot expose an unredacted
// original.
func (c *Client) fail(what string, detail any) error {
	msg := what
	if s := fmt.Sprint(detail); s != "" {
		msg += ": " + s
	}
	return errors.New(c.redact(msg))
}

func (c *Client) redact(s string) string {
	if c.APIKey != "" {
		s = strings.ReplaceAll(s, c.APIKey, redacted)
	}
	if c.Redact != nil {
		s = c.Redact(s)
	}
	return s
}
