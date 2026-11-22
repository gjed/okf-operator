package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// signaturePrefix starts every X-Hub-Signature-256 value.
const signaturePrefix = "sha256="

// VerifySignature reports whether header is the X-Hub-Signature-256 of body
// under secret. The comparison is constant-time; an empty secret never
// verifies.
func VerifySignature(secret, body []byte, header string) bool {
	if len(secret) == 0 || !strings.HasPrefix(header, signaturePrefix) {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, signaturePrefix))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign returns the X-Hub-Signature-256 value of body under secret.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}
