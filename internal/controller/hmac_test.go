package controller

import "testing"

func TestVerifySignature(t *testing.T) {
	// The example from GitHub's "Validating webhook deliveries" guide.
	secret, body := []byte("It's a Secret to Everybody"), []byte("Hello, World!")
	good := "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := Sign(secret, body); got != good {
		t.Fatalf("Sign = %s, want %s", got, good)
	}
	tests := []struct {
		name   string
		secret []byte
		body   []byte
		header string
		want   bool
	}{
		{"valid", secret, body, good, true},
		{"wrong secret", []byte("another secret"), body, good, false},
		{"tampered body", secret, []byte("Hello, World?"), good, false},
		{"missing header", secret, body, "", false},
		{"sha1 header", secret, body, "sha1=01dc10d0c83e72ed246219cdd91669667fe2ca59", false},
		{"no prefix", secret, body, good[len("sha256="):], false},
		{"not hex", secret, body, "sha256=zz", false},
		{"truncated", secret, body, good[:len(good)-2], false},
		{"empty secret", nil, body, Sign(nil, body), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifySignature(tt.secret, tt.body, tt.header); got != tt.want {
				t.Fatalf("VerifySignature = %v, want %v", got, tt.want)
			}
		})
	}
}
