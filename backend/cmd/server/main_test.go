package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestValidateMetadata(t *testing.T) {
	good := createUploadRequest{SenderID: "test", AudioFormat: "wav", Call: callMetadata{StartTime: time.Now(), DurationMS: 1000, SystemID: "system", TalkgroupID: "100"}}
	if err := validateMetadata(good); err != nil {
		t.Fatal(err)
	}
	good.AudioFormat = "flac"
	if validateMetadata(good) == nil {
		t.Fatal("expected format rejection")
	}
}
func TestValidateAudioHeader(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "a.wav")
	if err := os.WriteFile(wav, []byte("RIFFxxxxWAVEdata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := validateAudioHeader(wav, "wav"); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "bad.mp3")
	if err := os.WriteFile(bad, []byte("not audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if validateAudioHeader(bad, "mp3") == nil {
		t.Fatal("expected header rejection")
	}
}
func TestContentTypeMatches(t *testing.T) {
	if !contentTypeMatches("mp3", "audio/mpeg; charset=binary") {
		t.Fatal("mp3 content type")
	}
	if contentTypeMatches("wav", "audio/mpeg") {
		t.Fatal("mismatch accepted")
	}
}
