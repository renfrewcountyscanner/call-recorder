package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	base := flag.String("server", "", "Call Recorder server URL")
	sender := flag.String("sender", "", "sender ID")
	key := flag.String("key", "", "sender API key")
	metadata := flag.String("metadata", "", "call metadata JSON file")
	audio := flag.String("audio", "", "MP3 or WAV file")
	flag.Parse()
	if *base == "" || *sender == "" || *key == "" || *metadata == "" || *audio == "" {
		flag.Usage()
		os.Exit(2)
	}
	raw, err := os.ReadFile(*metadata)
	must(err)
	format := strings.TrimPrefix(strings.ToLower(filepath.Ext(*audio)), ".")
	if format != "mp3" && format != "wav" {
		must(fmt.Errorf("audio must be mp3 or wav"))
	}
	body, err := json.Marshal(request{SenderID: *sender, IdempotencyKey: filepath.Base(*audio), AudioFormat: format, Call: raw})
	must(err)
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(*base, "/")+"/api/v1/uploads", bytes.NewReader(body))
	must(err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Call-Recorder-Key", *key)
	client := &http.Client{}
	res, err := client.Do(req)
	must(err)
	defer res.Body.Close()
	var accepted response
	must(json.NewDecoder(res.Body).Decode(&accepted))
	if res.StatusCode/100 != 2 {
		must(fmt.Errorf("metadata rejected: %s", accepted.Error))
	}
	if accepted.Duplicate {
		fmt.Printf("duplicate call: %s\n", accepted.CallID)
		return
	}
	f, err := os.Open(*audio)
	must(err)
	defer f.Close()
	audioReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(*base, "/")+"/api/v1/uploads/"+accepted.UploadToken, io.Reader(f))
	must(err)
	audioReq.Header.Set("X-Call-Recorder-Sender", *sender)
	audioReq.Header.Set("X-Call-Recorder-Key", *key)
	if format == "mp3" {
		audioReq.Header.Set("Content-Type", "audio/mpeg")
	} else {
		audioReq.Header.Set("Content-Type", "audio/wav")
	}
	audioRes, err := client.Do(audioReq)
	must(err)
	defer audioRes.Body.Close()
	var completed response
	must(json.NewDecoder(audioRes.Body).Decode(&completed))
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
