package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type request struct {
	SenderID       string          `json:"sender_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	AudioFormat    string          `json:"audio_format"`
	Call           json.RawMessage `json:"call"`
}
type response struct {
	UploadToken string `json:"upload_token"`
	Duplicate   bool   `json:"duplicate"`
	CallID      string `json:"call_id"`
	Error       string `json:"error"`
}

func main() {
	base := flag.String("server", "", "Call Logger server URL")
	sender := flag.String("sender", "", "sender ID")
	key := flag.String("key", "", "deprecated: sender API key (prefer --key-file or CALL_LOGGER_API_KEY)")
	keyFile := flag.String("key-file", "", "path to a sender API key file")
	metadata := flag.String("metadata", "", "call metadata JSON file")
	audio := flag.String("audio", "", "MP3 or WAV file")
	flag.Parse()
	credential := strings.TrimSpace(os.Getenv("CALL_LOGGER_API_KEY"))
	if *keyFile != "" {
		rawKey, readErr := os.ReadFile(*keyFile)
		must(readErr)
		if credential != "" {
			must(fmt.Errorf("configure only one of --key-file or CALL_LOGGER_API_KEY"))
		}
		credential = strings.TrimSpace(string(rawKey))
	}
	if *key != "" {
		if credential != "" {
			must(fmt.Errorf("configure only one API key source"))
		}
		fmt.Fprintln(os.Stderr, "warning: --key exposes the credential in process listings; use --key-file")
		credential = *key
	}
	if *base == "" || *sender == "" || credential == "" || *metadata == "" || *audio == "" {
		flag.Usage()
		os.Exit(2)
	}
	parsedBase, err := url.Parse(strings.TrimRight(*base, "/"))
	must(err)
	if parsedBase.Scheme != "https" && !(parsedBase.Scheme == "http" && (parsedBase.Hostname() == "127.0.0.1" || parsedBase.Hostname() == "localhost" || parsedBase.Hostname() == "::1")) {
		must(fmt.Errorf("server must use HTTPS unless it is localhost"))
	}
	raw, err := os.ReadFile(*metadata)
	must(err)
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(*audio)), ".")
	if format != "mp3" && format != "wav" {
		must(fmt.Errorf("audio must be mp3 or wav"))
	}
	audioFile, err := os.Open(*audio)
	must(err)
	digest := sha256.New()
	_, err = io.Copy(digest, audioFile)
	must(err)
	_, _ = digest.Write(raw)
	idempotencyKey := fmt.Sprintf("cli-%x", digest.Sum(nil)[:16])
	_, err = audioFile.Seek(0, io.SeekStart)
	must(err)
	defer audioFile.Close()
	body, err := json.Marshal(request{SenderID: *sender, IdempotencyKey: idempotencyKey, AudioFormat: format, Call: raw})
	must(err)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*base, "/")+"/api/v1/uploads", bytes.NewReader(body))
	must(err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Call-Recorder-Key", credential)
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return fmt.Errorf("redirects are disabled") }}
	res, err := client.Do(req)
	must(err)
	defer res.Body.Close()
	var accepted response
	must(json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&accepted))
	if res.StatusCode/100 != 2 {
		must(fmt.Errorf("metadata rejected: %s", accepted.Error))
	}
	if accepted.Duplicate {
		fmt.Printf("duplicate call: %s\n", accepted.CallID)
		return
	}
	audioReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(*base, "/")+"/api/v1/uploads/"+accepted.UploadToken, audioFile)
	must(err)
	if info, statErr := audioFile.Stat(); statErr == nil {
		audioReq.ContentLength = info.Size()
	}
	audioReq.Header.Set("X-Call-Recorder-Sender", *sender)
	audioReq.Header.Set("X-Call-Recorder-Key", credential)
	if format == "mp3" {
		audioReq.Header.Set("Content-Type", "audio/mpeg")
	} else {
		audioReq.Header.Set("Content-Type", "audio/wav")
	}
	audioRes, err := client.Do(audioReq)
	must(err)
	defer audioRes.Body.Close()
	var completed response
	must(json.NewDecoder(io.LimitReader(audioRes.Body, 1<<20)).Decode(&completed))
	if audioRes.StatusCode/100 != 2 {
		must(fmt.Errorf("audio rejected: %s", completed.Error))
	}
	fmt.Printf("completed call: %s\n", completed.CallID)
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
