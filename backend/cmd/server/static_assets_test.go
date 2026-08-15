package main

import (
	"strings"
	"testing"
)

func TestConfirmedFormPreservesSubmitter(t *testing.T) {
	body, err := staticFS.ReadFile("web/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	script := string(body)
	for _, expected := range []string{
		"pendingSubmitter = e.submitter || null",
		"pendingForm.requestSubmit(pendingSubmitter || undefined)",
		"button.getAttribute('data-submit-confirm')",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("confirmation handler is missing %q", expected)
		}
	}
}
