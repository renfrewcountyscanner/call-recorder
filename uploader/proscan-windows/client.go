package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type loggerClient struct {
	baseURL, senderID, apiKey string
	httpClient                *http.Client
}

func newLoggerClient(cfg config) (*loggerClient, error) {
	sender, key, err := cfg.credentialsForWatch(watchConfig{})
	if err != nil {
		return nil, err
	}
	return newLoggerClientWithCredentials(cfg, sender, key), nil
}

func newLoggerClientForWatch(cfg config, watch watchConfig) (*loggerClient, error) {
	sender, key, err := cfg.credentialsForWatch(watch)
	if err != nil {
		return nil, err
	}
	return newLoggerClientWithCredentials(cfg, sender, key), nil
}

func newLoggerClientWithCredentials(cfg config, sender, key string) *loggerClient {
	client := &http.Client{
		Timeout: time.Duration(cfg.Logger.RequestTimeoutSeconds) * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("unexpected redirect; machine ingestion must bypass interactive login")
		},
	}
	return &loggerClient{baseURL: strings.TrimRight(cfg.Logger.URL, "/"), senderID: sender, apiKey: key, httpClient: client}
}

func (client *loggerClient) Upload(ctx context.Context, item *spoolItem) (string, bool, error) {
	raw, err := json.Marshal(item.Request)
	if err != nil {
		return "", false, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/v1/uploads", bytes.NewReader(raw))
	if err != nil {
		return "", false, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Call-Recorder-Key", client.apiKey)
	request.Header.Set("User-Agent", "call-recorder-proscan-uploader/1.0")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return "", false, fmt.Errorf("metadata request: %w", err)
	}
	accepted, err := decodeUploadResponse(response)
	if err != nil {
		return "", false, fmt.Errorf("metadata request: %w", err)
	}
	if accepted.Duplicate {
		return accepted.CallID, true, nil
	}
	if accepted.UploadToken == "" {
		return "", false, errors.New("metadata accepted without an upload token")
	}
	audio, err := os.Open(filepathJoin(item.directory, item.AudioFile))
	if err != nil {
		return "", false, fmt.Errorf("open spooled audio: %w", err)
	}
	defer audio.Close()
	audioRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+"/api/v1/uploads/"+accepted.UploadToken, audio)
	if err != nil {
		return "", false, err
	}
	audioRequest.Header.Set("Content-Type", "audio/mpeg")
	audioRequest.Header.Set("X-Call-Recorder-Sender", client.senderID)
	audioRequest.Header.Set("X-Call-Recorder-Key", client.apiKey)
	audioRequest.Header.Set("User-Agent", "call-recorder-proscan-uploader/1.0")
	audioResponse, err := client.httpClient.Do(audioRequest)
	if err != nil {
		return "", false, fmt.Errorf("audio request: %w", err)
	}
	completed, err := decodeUploadResponse(audioResponse)
	if err != nil {
		return "", false, fmt.Errorf("audio request: %w", err)
	}
	return completed.CallID, completed.Duplicate, nil
}

func decodeUploadResponse(response *http.Response) (uploadResponse, error) {
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return uploadResponse{}, err
	}
	var decoded uploadResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return uploadResponse{}, fmt.Errorf("logger returned HTTP %d with a non-JSON response", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := strings.TrimSpace(decoded.Error)
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		return uploadResponse{}, fmt.Errorf("logger returned HTTP %d: %s", response.StatusCode, message)
	}
	if decoded.Error != "" {
		return uploadResponse{}, errors.New(decoded.Error)
	}
	return decoded, nil
}

// Kept as a variable so tests can exercise platform-neutral spooling without
// shadowing filepath imports in multiple build-tagged files.
var filepathJoin = func(parts ...string) string {
	return strings.Join(parts, string(os.PathSeparator))
}
