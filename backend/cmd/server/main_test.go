package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGeneratedSenderKeyIsUUIDv4(t *testing.T) {
	key, err := generateKey()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(key) {
		t.Fatalf("generated key is not UUIDv4: %q", key)
	}
}

func TestSenderKeyHMACAndLegacyCompatibility(t *testing.T) {
	encoded, err := hashSenderKey("test-pepper", "sender-key")
	if err != nil {
		t.Fatal(err)
	}
	if !verifySenderKey("test-pepper", encoded, "sender-key") {
		t.Fatal("HMAC sender key was rejected")
	}
	if verifySenderKey("wrong-pepper", encoded, "sender-key") || verifySenderKey("test-pepper", encoded, "wrong-key") {
		t.Fatal("invalid HMAC sender credential was accepted")
	}
	legacy, err := hashAPIKey("legacy-key")
	if err != nil {
		t.Fatal(err)
	}
	if !verifySenderKey("test-pepper", legacy, "legacy-key") {
		t.Fatal("legacy Argon2 sender credential was rejected during migration")
	}
}

func TestArgonParametersAreBounded(t *testing.T) {
	if verifyAPIKey("argon2id$v=19$m=4294967295,t=3,p=2$0011223344556677$00112233445566778899aabbccddeeff", "key") {
		t.Fatal("unbounded Argon2 parameters were accepted")
	}
}

func TestLegacyNullValue(t *testing.T) {
	for _, value := range []any{nil, "<nil>", " NULL "} {
		if !legacyNullValue(value) {
			t.Fatalf("expected %v to be legacy null", value)
		}
	}
	for _, value := range []any{"", "1234", 0} {
		if legacyNullValue(value) {
			t.Fatalf("unexpected legacy null for %v", value)
		}
	}
}

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

func TestShouldQuarantineLegacyAudio(t *testing.T) {
	first := time.Date(2026, time.August, 16, 18, 0, 0, 0, time.UTC)
	if shouldQuarantineLegacyAudio(9, first, first.Add(time.Minute), 10, 30*time.Second) {
		t.Fatal("quarantined before the identical failure threshold")
	}
	if shouldQuarantineLegacyAudio(10, first, first.Add(29*time.Second), 10, 30*time.Second) {
		t.Fatal("quarantined before the grace period")
	}
	if !shouldQuarantineLegacyAudio(10, first, first.Add(30*time.Second), 10, 30*time.Second) {
		t.Fatal("did not quarantine after both safeguards were met")
	}
}

func TestStorageStatsUsesConfiguredAudioFilesystem(t *testing.T) {
	dir := t.TempDir()
	s := &server{cfg: config{AudioRoot: dir}}
	stats, err := s.storageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalBytes == 0 || stats.FreeBytes == 0 || stats.FreeBytes > stats.TotalBytes {
		t.Fatalf("invalid filesystem stats: %#v", stats)
	}
	if stats.FreePct < 0 || stats.FreePct > 100 || stats.UsedPct < 0 || stats.UsedPct > 100 {
		t.Fatalf("invalid percentages: %#v", stats)
	}
}

func TestFormatBytesIsHumanReadable(t *testing.T) {
	for value, want := range map[uint64]string{0: "0 B", 1024: "1.0 KiB", 1024 * 1024: "1.0 MiB"} {
		if got := formatBytes(value); got != want {
			t.Fatalf("formatBytes(%d) = %q, want %q", value, got, want)
		}
	}
	if got := formatBytes(int64(722337157)); got != "688.9 MiB" {
		t.Fatalf("formatBytes accepts PostgreSQL bigint values: got %q", got)
	}
}

func TestCloudflareIdentityMapsAdminAndViewer(t *testing.T) {
	s := &server{cfg: config{CloudflareAccessEnabled: true, CloudflareAdminEmail: "admin@example.test", CloudflareTrustedProxyIPs: []string{"192.0.2.10"}}}
	adminReq := httptest.NewRequest("GET", "/", nil)
	adminReq.RemoteAddr = "192.0.2.10:443"
	adminReq.Header.Set("Cf-Access-Authenticated-User-Email", "admin@example.test")
	if !s.adminOK(adminReq) || s.getUserRole(adminReq) != "admin" {
		t.Fatalf("admin identity was not mapped: role=%q", s.getUserRole(adminReq))
	}
	viewerReq := httptest.NewRequest("GET", "/", nil)
	viewerReq.RemoteAddr = "192.0.2.10:443"
	viewerReq.Header.Set("Cf-Access-Authenticated-User-Email", "guest@example.test")
	if !s.adminOK(viewerReq) || s.getUserRole(viewerReq) != "viewer" {
		t.Fatalf("viewer identity was not mapped: role=%q", s.getUserRole(viewerReq))
	}
	spoofed := httptest.NewRequest("GET", "/", nil)
	spoofed.RemoteAddr = "198.51.100.8:443"
	spoofed.Header.Set("Cf-Access-Authenticated-User-Email", "admin@example.test")
	if s.adminOK(spoofed) {
		t.Fatal("untrusted Cloudflare identity was accepted")
	}
}

func TestValidCSRFToken(t *testing.T) {
	token := "csrf-test-token"
	req := httptest.NewRequest(http.MethodPost, "/admin/action", strings.NewReader("csrf_token="+url.QueryEscape(token)))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !validCSRFToken(req, tokenHash(token)) {
		t.Fatal("valid form CSRF token was rejected")
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/action", nil)
	req.Header.Set("X-CSRF-Token", token)
	if !validCSRFToken(req, tokenHash(token)) {
		t.Fatal("valid header CSRF token was rejected")
	}
	req.Header.Set("X-CSRF-Token", "wrong")
	if validCSRFToken(req, tokenHash(token)) {
		t.Fatal("invalid CSRF token was accepted")
	}
}
