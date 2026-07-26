package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterFromQueryIncludesPhase7Fields(t *testing.T) {
	f, err := filterFromQuery(url.Values{"q": {"dispatch"}, "sender": {"s"}, "system": {"sys"}, "site": {"site-1"}, "receiver": {"rx"}, "talkgroup": {"100"}, "radio": {"200"}, "call_type": {"private"}, "frequency": {"851"}, "min_duration": {"1.5"}, "max_duration": {"30"}, "patched": {"1"}, "page": {"2"}, "page_size": {"100"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.Site != "site-1" || f.Receiver != "rx" || f.CallType != "private" || !f.Patched || f.Page != 2 || f.PageSize != 100 {
		t.Fatalf("unexpected filter: %#v", f)
	}
	u := callsURL(f, "", f.Page)
	for _, want := range []string{"site=site-1", "receiver=rx", "call_type=private", "patched=1", "page=2"} {
		if !strings.Contains(u, want) {
			t.Fatalf("%q missing from %s", want, u)
		}
	}
}

func TestFilterRejectsInvalidDuration(t *testing.T) {
	if _, err := filterFromQuery(url.Values{"min_duration": {"-1"}}); err == nil {
		t.Fatal("expected invalid duration")
	}
}

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
