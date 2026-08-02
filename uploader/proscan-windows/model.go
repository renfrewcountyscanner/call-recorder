package main

import "time"

type createUploadRequest struct {
	SenderID       string       `json:"sender_id"`
	IdempotencyKey string       `json:"idempotency_key,omitempty"`
	AudioFormat    string       `json:"audio_format"`
	Call           callMetadata `json:"call"`
}

type callMetadata struct {
	SourceCallID  string `json:"source_call_id,omitempty"`
	StartTime     string `json:"start_time"`
	DurationMS    int64  `json:"duration_ms"`
	ReceiverID    string `json:"receiver_id,omitempty"`
	SystemID      string `json:"system_id"`
	SystemName    string `json:"system_name,omitempty"`
	SiteID        string `json:"site_id,omitempty"`
	SiteName      string `json:"site_name,omitempty"`
	TalkgroupID   string `json:"talkgroup_id"`
	TalkgroupName string `json:"talkgroup_name,omitempty"`
	TalkgroupTag  string `json:"talkgroup_tag,omitempty"`
	RadioID       string `json:"radio_id,omitempty"`
	Frequency     string `json:"frequency,omitempty"`
	VoiceService  string `json:"voice_service,omitempty"`
	CallType      string `json:"call_type,omitempty"`
	GroupCall     *bool  `json:"group_call,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

type parsedRecording struct {
	Request       createUploadRequest
	AudioBytes    []byte
	AudioFormat   string
	OriginalName  string
	Embedded      proScanMetadata
	AudioDuration time.Duration
}

type proScanMetadata struct {
	Start, End                                      time.Time
	Scanner, Favorite, System, Site, Department     string
	Channel, Frequency, Modulation, Tone, TGID, UID string
	RSSI, ServiceType, DigitalStatus, DMRSlot       string
}

type uploadResponse struct {
	UploadToken string `json:"upload_token"`
	Duplicate   bool   `json:"duplicate"`
	Retryable   bool   `json:"retryable"`
	CallID      string `json:"call_id"`
	Status      string `json:"status"`
	Error       string `json:"error"`
}
