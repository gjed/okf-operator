package model

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const okBody = `{"choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":3}}`

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestCompleteRequestShape(t *testing.T) {
	var gotPath, gotMethod, gotCT, gotAuth string
	var gotBody map[string]any
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotCT, gotAuth = r.Header.Get("Content-Type"), r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("body is not JSON: %v", err)
		}
		_, _ = io.WriteString(w, okBody)
	})

	c := &Client{BaseURL: srv.URL + "/v1", Model: "m1", APIKey: "sk-test-123456"}
	reply, err := c.Complete(context.Background(), Request{System: "sys", User: "usr", MaxTokens: 77})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST /v1/chat/completions", gotMethod, gotPath)
	}
	if gotCT != "application/json" {
		t.Errorf("Content-Type = %q", gotCT)
	}
	if gotAuth != "Bearer sk-test-123456" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotBody["model"] != "m1" || gotBody["stream"] != false {
		t.Errorf("model/stream = %v/%v", gotBody["model"], gotBody["stream"])
	}
	if gotBody["temperature"] != float64(0) {
		t.Errorf("temperature = %v, want 0", gotBody["temperature"])
	}
	if gotBody["max_tokens"] != float64(77) {
		t.Errorf("max_tokens = %v, want 77", gotBody["max_tokens"])
	}
	if _, ok := gotBody["tools"]; ok {
		t.Error("request carries tools")
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 ||
		msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "sys" ||
		msgs[1].(map[string]any)["role"] != "user" || msgs[1].(map[string]any)["content"] != "usr" {
		t.Errorf("messages = %v", msgs)
	}
	if reply.Text != "hello" || reply.FinishReason != "stop" {
		t.Errorf("reply = %+v", reply)
	}
	if reply.Usage != (Usage{Prompt: 11, Completion: 3, Reported: true}) {
		t.Errorf("usage = %+v", reply.Usage)
	}
}

func TestCompleteNoAuthorizationWithoutKey(t *testing.T) {
	var auth []string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Values("Authorization")...)
		_, _ = io.WriteString(w, okBody)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	if _, err := c.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(auth) != 0 {
		t.Errorf("Authorization headers sent without a key: %v", auth)
	}
}

func TestCompleteFinishReasonLength(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}],"usage":{"prompt_tokens":1,"completion_tokens":9}}`)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	reply, err := c.Complete(context.Background(), Request{MaxTokens: 9})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if reply.FinishReason != "length" || reply.Text != "partial" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestCompleteMissingUsage(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	reply, err := c.Complete(context.Background(), Request{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if reply.Usage.Reported {
		t.Errorf("Usage.Reported = true with no usage in reply")
	}
	if reply.Text != "x" {
		t.Errorf("Text = %q", reply.Text)
	}
}

func TestCompleteHTTP500(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream exploded", http.StatusInternalServerError)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	_, err := c.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "upstream exploded") {
		t.Errorf("error = %q, want status and body", err)
	}
}

func TestCompleteHTTP401DoesNotEchoKey(t *testing.T) {
	const key = "sk-secret-key-9999"
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid key `+key+`"}`)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1", APIKey: key}
	_, err := c.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), key) {
		t.Errorf("error leaks key: %q", err)
	}
	if !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Errorf("error = %q, want 401 and redaction marker", err)
	}
}

func TestCompleteRedactHookApplied(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "token ghp_abcdefgh1234 rejected")
	})
	c := &Client{BaseURL: srv.URL, Model: "m1", Redact: func(s string) string {
		return strings.ReplaceAll(s, "ghp_abcdefgh1234", "[GH]")
	}}
	_, err := c.Complete(context.Background(), Request{})
	if err == nil || strings.Contains(err.Error(), "ghp_abcdefgh1234") || !strings.Contains(err.Error(), "[GH]") {
		t.Errorf("error = %v, want redacted via hook", err)
	}
}

func TestCompleteErrorBodyBounded(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, strings.Repeat("a", 4000)+"TAIL")
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	_, err := c.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "TAIL") {
		t.Error("error body not truncated to 512 bytes")
	}
	if len(err.Error()) > 512+len("model endpoint returned HTTP 400: ") {
		t.Errorf("error length %d exceeds bound", len(err.Error()))
	}
}

func TestCompleteTrailingSlashBaseURL(t *testing.T) {
	var path string
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, okBody)
	})
	c := &Client{BaseURL: srv.URL + "/v1/", Model: "m1"}
	if _, err := c.Complete(context.Background(), Request{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if path != "/v1/chat/completions" {
		t.Errorf("path = %q", path)
	}
}

func TestCompleteZeroChoices(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	})
	c := &Client{BaseURL: srv.URL, Model: "m1"}
	if _, err := c.Complete(context.Background(), Request{}); err == nil {
		t.Fatal("want error for zero choices")
	}
}

func TestCompleteTransportErrorRedacted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // connection refused
	c := &Client{BaseURL: srv.URL, Model: "m1", APIKey: "sk-transport-key-1"}
	_, err := c.Complete(context.Background(), Request{})
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), "sk-transport-key-1") {
		t.Errorf("error leaks key: %q", err)
	}
}
