package main

import "testing"

func TestDirectNotificationURLBlocksPrivateAddresses(t *testing.T) {
	t.Setenv("CALL_RECORDER_ALLOW_PRIVATE_DESTINATIONS", "false")
	for _, raw := range []string{
		"http://127.0.0.1/hook",
		"http://[::1]/hook",
		"http://localhost/hook",
		"http://169.254.169.254/latest",
		"ftp://example.invalid/file",
		"https://user:password@example.invalid/hook",
	} {
		if err := validateDirectNotificationURL(raw); err == nil {
			t.Fatalf("expected destination to be rejected: %s", raw)
		}
	}
	if err := validateDirectNotificationURL("https://example.invalid/hook"); err != nil {
		t.Fatalf("public URL rejected: %v", err)
	}
}

func TestDirectNotificationClientDisablesRedirects(t *testing.T) {
	client := safeDirectNotificationClient()
	if client.CheckRedirect == nil {
		t.Fatal("redirect policy is not configured")
	}
	if err := client.CheckRedirect(nil, nil); err == nil {
		t.Fatal("notification client accepted a redirect")
	}
}
