//go:build windows

package winapi

import (
	"bytes"
	"testing"
)

func TestDPAPI_RoundTrip(t *testing.T) {
	t.Parallel()
	plaintext := []byte("hashpoint-msal-cache-v1: {\"foo\":\"bar\"}")
	ct, err := ProtectDataCurrentUser(plaintext)
	if err != nil {
		t.Fatalf("ProtectDataCurrentUser: %v", err)
	}
	if bytes.Equal(ct, plaintext) {
		t.Fatal("ciphertext equals plaintext — DPAPI not actually encrypting")
	}
	pt, err := UnprotectDataCurrentUser(ct)
	if err != nil {
		t.Fatalf("UnprotectDataCurrentUser: %v", err)
	}
	if !bytes.Equal(pt, plaintext) {
		t.Fatalf("round-trip mismatch: got %q want %q", pt, plaintext)
	}
}

func TestDPAPI_EmptyInputErrors(t *testing.T) {
	t.Parallel()
	if _, err := ProtectDataCurrentUser(nil); err == nil {
		t.Error("expected error for empty plaintext")
	}
	if _, err := UnprotectDataCurrentUser(nil); err == nil {
		t.Error("expected error for empty ciphertext")
	}
}

func TestDPAPI_TamperedCiphertextRejected(t *testing.T) {
	t.Parallel()
	ct, err := ProtectDataCurrentUser([]byte("payload"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	// Flip a byte in the middle. DPAPI checks integrity via HMAC and must
	// reject the modified blob — we treat any error here as "cache invalid".
	ct[len(ct)/2] ^= 0xFF
	if _, err := UnprotectDataCurrentUser(ct); err == nil {
		t.Fatal("expected error decrypting tampered blob, got nil")
	}
}
