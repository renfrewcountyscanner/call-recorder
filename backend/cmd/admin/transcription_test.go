package main

import "testing"

func TestTranscriptionEndpointAllowlistValidation(t *testing.T) {
	if _, err := transcriptionHTTPClient("http://192.168.2.2:9912/v1/audio/transcriptions", "192.168.2.2/32"); err != nil {
		t.Fatal(err)
	}
	if _, err := transcriptionHTTPClient("http://192.168.2.2:9912/v1/audio/transcriptions", "192.168.2.3/32"); err == nil {
		t.Fatal("expected blocked endpoint when CIDR does not include host")
	}
	if _, err := transcriptionHTTPClient("http://127.0.0.1:9912/transcribe", "127.0.0.1/32"); err == nil {
		t.Fatal("loopback must remain blocked")
	}
	if _, err := transcriptionHTTPClient("http://user:pass@example.invalid/transcribe", ""); err == nil {
		t.Fatal("embedded credentials must be rejected")
	}
}
