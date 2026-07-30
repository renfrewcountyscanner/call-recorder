package main

import (
	"testing"

	"github.com/renfrewcountyscanner/call-recorder/backend/internal/transcription"
)

func TestTranscriptionEndpointAllowlistValidation(t *testing.T) {
	if _, err := transcription.HTTPClient("http://192.168.2.2:9912/v1/audio/transcriptions", "192.168.2.2/32"); err != nil {
		t.Fatal(err)
	}
	if _, err := transcription.HTTPClient("http://192.168.2.2:9912/v1/audio/transcriptions", "192.168.2.3/32"); err == nil {
		t.Fatal("expected blocked endpoint when CIDR does not include host")
	}
	if _, err := transcription.HTTPClient("http://127.0.0.1:9912/transcribe", "127.0.0.1/32"); err == nil {
		t.Fatal("loopback must remain blocked")
	}
	if _, err := transcription.HTTPClient("http://user:pass@example.invalid/transcribe", ""); err == nil {
		t.Fatal("embedded credentials must be rejected")
	}
}

func TestTranscriptionAllowedCIDRParsing(t *testing.T) {
	if _, err := transcription.ParseAllowedCIDRs("192.168.2.2/32, 10.0.0.0/8"); err != nil {
		t.Fatal(err)
	}
	if _, err := transcription.ParseAllowedCIDRs("not-a-cidr"); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
}

func TestSyntheticWAVIsValid(t *testing.T) {
	wav, err := transcription.SyntheticWAV()
	if err != nil {
		t.Fatal(err)
	}
	if len(wav) < 44 {
		t.Fatalf("WAV too short: %d", len(wav))
	}
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatal("WAV header missing")
	}
}
