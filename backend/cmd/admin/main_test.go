package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSenderCredentialFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sender.key")
	if err := os.WriteFile(path, []byte("aa081a7d-ad2f-4194-a8ec-c85a0447f140\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, supplied, err := senderCredential(path)
	if err != nil {
		t.Fatal(err)
	}
	if !supplied || key != "aa081a7d-ad2f-4194-a8ec-c85a0447f140" {
		t.Fatalf("unexpected result supplied=%v key=%q", supplied, key)
	}
}

func TestSenderCredentialRejectsWhitespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sender.key")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 16)+" bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := senderCredential(path); err == nil {
		t.Fatal("expected whitespace-containing credential to be rejected")
	}
}
