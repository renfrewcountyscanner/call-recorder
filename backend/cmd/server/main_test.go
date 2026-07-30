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

func TestFilterSortIsValidatedAndShareable(t *testing.T) {
	f, err := filterFromQuery(url.Values{"sort": {"duration"}, "page_size": {"250"}})
	if err != nil || f.Sort != "duration" || f.PageSize != 250 {
		t.Fatalf("unexpected sort filter: %#v err=%v", f, err)
	}
	if !strings.Contains(callsURL(f, "", 1), "sort=duration") {
		t.Fatalf("sort missing from URL: %s", callsURL(f, "", 1))
	}
	unknown, err := filterFromQuery(url.Values{"sort": {"unsafe-column"}})
	if err != nil || unknown.Sort != "newest" {
		t.Fatalf("unsafe sort was not normalized: %#v err=%v", unknown, err)
	}
}

func TestAdditionalSortsAreValidated(t *testing.T) {
	for _, sort := range []string{"system", "site", "calltype", "lcn", "receiver", "talkgroup_label", "radio_label"} {
		f, err := filterFromQuery(url.Values{"sort": {sort}})
		if err != nil || f.Sort != sort {
			t.Fatalf("sort %q was not accepted: %#v err=%v", sort, f, err)
		}
	}
}

func TestFilterSupportsRepeatedAndCommaSeparatedValues(t *testing.T) {
	f, err := filterFromQuery(url.Values{"system": {"alpha", "beta,gamma"}, "talkgroup": {"100,200"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.System != "alpha,beta,gamma" || f.Talkgroup != "100,200" {
		t.Fatalf("multi-value filters were not normalized: %#v", f)
	}
	u := callsURL(f, "", 1)
	if !strings.Contains(u, "system=alpha%2Cbeta%2Cgamma") || !strings.Contains(u, "talkgroup=100%2C200") {
		t.Fatalf("multi-value filters not shareable: %s", u)
	}
}

func TestFilterSupportsCallClass(t *testing.T) {
	f, err := filterFromQuery(url.Values{"group": {"private"}})
	if err != nil || f.Group != "private" {
		t.Fatalf("call class was not parsed: %#v err=%v", f, err)
	}
	if !strings.Contains(callsURL(f, "", 1), "group=private") {
		t.Fatalf("call class was not shareable: %s", callsURL(f, "", 1))
	}
	unsafe, _ := filterFromQuery(url.Values{"group": {"sql"}})
	if unsafe.Group != "" {
		t.Fatalf("unsafe call class accepted: %#v", unsafe)
	}
}

func TestSmartSortIsShareable(t *testing.T) {
	f, err := filterFromQuery(url.Values{"smart_sort": {"1"}})
	if err != nil || !f.SmartSort {
		t.Fatalf("smart sort not parsed: %#v err=%v", f, err)
	}
	if !strings.Contains(callsURL(f, "", 1), "smart_sort=1") {
		t.Fatalf("smart sort missing from URL: %s", callsURL(f, "", 1))
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

func TestApplicationSecretEncryptionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	key, err := loadSecretMasterKey(dir)
	if err != nil || len(key) != secretKeySize {
		t.Fatalf("master key: %v", err)
	}
	if got, _ := os.Stat(filepath.Join(dir, "master.key")); got.Mode().Perm() != 0o600 {
		t.Fatalf("master key permissions: %o", got.Mode().Perm())
	}
	ciphertext, nonce, err := encryptSecret(key, []byte("synthetic-key"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decryptSecret(key, ciphertext, nonce)
	if err != nil || string(plain) != "synthetic-key" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	if _, err := decryptSecret(make([]byte, secretKeySize), ciphertext, nonce); err == nil {
		t.Fatal("wrong key decrypted secret")
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
