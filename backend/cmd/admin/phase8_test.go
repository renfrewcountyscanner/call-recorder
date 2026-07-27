package main

import "testing"

func TestNotificationURLBlocksPrivateAddresses(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1/hook", "http://localhost/hook", "http://169.254.169.254/latest"} {
		if err := validateNotificationURL(raw); err == nil {
			t.Fatalf("expected private URL to be rejected: %s", raw)
		}
	}
	if err := validateNotificationURL("https://example.invalid/hook"); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
}
