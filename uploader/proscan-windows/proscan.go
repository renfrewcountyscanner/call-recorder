package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	timestampFirstName = regexp.MustCompile(`^(\d{2}-\d{2}-\d{2} \d{2}-\d{2}-\d{2}) - `)
	channelFirstName   = regexp.MustCompile(`_(\d{8}_\d{2}-\d{2}-\d{2})(?:\.[^.]+)?$`)
	identifierCleaner  = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
)

func parseProScanRecording(audio []byte, filename string, watch watchConfig, senderID string, location *time.Location) (parsedRecording, error) {
	if len(audio) < 10 || string(audio[:3]) != "ID3" {
		return parsedRecording{}, errors.New("recording does not begin with an ID3 tag")
	}
	frames, custom, tagEnd, err := parseID3(audio)
	if err != nil {
		return parsedRecording{}, err
	}
	metadata := metadataFromTags(frames, custom, watch)
	metadata.Start, metadata.End, err = recordingTimes(frames["TRDA"], custom["EndingDate"], filename, location)
	if err != nil {
		return parsedRecording{}, err
	}
	duration, err := mp3Duration(audio, tagEnd)
	if err != nil {
		if metadata.End.After(metadata.Start) {
			duration = metadata.End.Sub(metadata.Start)
		} else {
			return parsedRecording{}, err
		}
	}
	if duration <= 0 {
		return parsedRecording{}, errors.New("recording duration is zero")
	}

	receiver := strings.TrimSpace(watch.ReceiverID)
	if receiver == "" {
		receiver = metadata.Scanner
	}
	talkgroupID := strings.TrimSpace(metadata.TGID)
	callType := "trunked"
	if talkgroupID == "" {
		callType = "conventional"
		talkgroupID = conventionalID(watch.ConventionalIDPrefix, metadata.Frequency, metadata.Tone, metadata.Channel)
	}
	if talkgroupID == "" {
		return parsedRecording{}, errors.New("recording has neither a TGID nor enough conventional-channel metadata")
	}
	siteID, siteName := "", ""
	if watch.proScanSystemAsSite() && metadata.System != "" {
		siteID = stableIdentifier(metadata.System)
		siteName = metadata.System
	}
	groupCall := true
	notes := proScanNotes(metadata)
	sourceMaterial := strings.Join([]string{
		watch.SystemID,
		metadata.Start.UTC().Format(time.RFC3339Nano),
		metadata.End.UTC().Format(time.RFC3339Nano),
		receiver,
		talkgroupID,
		metadata.Channel,
		metadata.Frequency,
		metadata.UID,
	}, "\x1f")
	digest := sha256.Sum256([]byte(sourceMaterial))
	sourceID := "proscan-" + hex.EncodeToString(digest[:16])
	request := createUploadRequest{
		SenderID:       senderID,
		IdempotencyKey: sourceID,
		AudioFormat:    "mp3",
		Call: callMetadata{
			SourceCallID:  sourceID,
			StartTime:     metadata.Start.UTC().Format(time.RFC3339Nano),
			DurationMS:    duration.Milliseconds(),
			ReceiverID:    receiver,
			SystemID:      strings.TrimSpace(watch.SystemID),
			SystemName:    strings.TrimSpace(watch.SystemName),
			SiteID:        siteID,
			SiteName:      siteName,
			TalkgroupID:   talkgroupID,
			TalkgroupName: metadata.Channel,
			TalkgroupTag:  metadata.Department,
			RadioID:       metadata.UID,
			Frequency:     metadata.Frequency,
			VoiceService:  metadata.Modulation,
			CallType:      callType,
			GroupCall:     &groupCall,
			Notes:         notes,
		},
	}
	return parsedRecording{Request: request, AudioBytes: audio, AudioFormat: "mp3", OriginalName: filepath.Base(filename), Embedded: metadata, AudioDuration: duration}, nil
}

func parseID3(audio []byte) (map[string]string, map[string]string, int, error) {
	if len(audio) < 10 || string(audio[:3]) != "ID3" {
		return nil, nil, 0, errors.New("missing ID3 header")
	}
	if audio[3] != 3 {
		return nil, nil, 0, fmt.Errorf("unsupported ID3 version 2.%d", audio[3])
	}
	tagSize := syncSafeInt(audio[6:10])
	tagEnd := 10 + tagSize
	if tagEnd > len(audio) || tagSize <= 0 {
		return nil, nil, 0, errors.New("invalid ID3 tag size")
	}
	frames := map[string]string{}
	for position := 10; position+10 <= tagEnd; {
		idBytes := audio[position : position+4]
		if !standardFrameID(idBytes) {
			break
		}
		size := int(uint32(audio[position+4])<<24 | uint32(audio[position+5])<<16 | uint32(audio[position+6])<<8 | uint32(audio[position+7]))
		if size <= 0 || position+10+size > tagEnd {
			break
		}
		id := string(idBytes)
		payload := audio[position+10 : position+10+size]
		if strings.HasPrefix(id, "T") {
			frames[id] = decodeID3Text(payload)
		}
		position += 10 + size
	}
	custom := map[string]string{}
	if start := bytes.Index(audio[10:tagEnd], []byte("Scanner:")); start >= 0 {
		block := audio[10+start : tagEnd]
		for _, part := range bytes.Split(block, []byte{0}) {
			key, value, found := strings.Cut(string(part), ":")
			if found && key != "" {
				custom[strings.TrimSpace(key)] = strings.TrimSpace(value)
			}
		}
	}
	return frames, custom, tagEnd, nil
}

func metadataFromTags(frames, custom map[string]string, watch watchConfig) proScanMetadata {
	title := splitPipe(frames["TIT2"])
	artist := splitPipe(frames["TPE1"])
	album := splitPipe(frames["TPE2"])
	value := func(key string, fallback []string, index int) string {
		if item := strings.TrimSpace(custom[key]); item != "" {
			return item
		}
		if index >= 0 && index < len(fallback) {
			return strings.TrimSpace(fallback[index])
		}
		return ""
	}
	uid := value("UID", nil, -1)
	if uid == "" {
		uid = value("UID#", nil, -1)
	}
	if uid == "" && watch.useTPE2RadioID() && len(album) > 6 {
		uid = strings.TrimSpace(album[6])
	}
	tgid, tgidPresent := custom["TGID"]
	if !tgidPresent && len(title) > 0 {
		tgid = title[0]
	}
	return proScanMetadata{
		Scanner:       value("Scanner", album, 1),
		Favorite:      value("FavoriteName", nil, -1),
		System:        value("SystemName", artist, 0),
		Site:          value("SiteName", nil, -1),
		Department:    value("DepartmentName", artist, 1),
		Channel:       value("ChannelName", title, 1),
		Frequency:     value("Frequency", album, 2),
		Modulation:    value("Modulation", album, 3),
		Tone:          value("Tone", album, 4),
		TGID:          strings.TrimSpace(tgid),
		UID:           uid,
		RSSI:          value("RSSI", nil, -1),
		ServiceType:   value("ServiceType", nil, -1),
		DigitalStatus: value("DigitalStatus", nil, -1),
		DMRSlot:       value("DMRSlot", nil, -1),
	}
}

func recordingTimes(rangeValue, endingValue, filename string, location *time.Location) (time.Time, time.Time, error) {
	trimmed := strings.TrimSpace(rangeValue)
	if len(trimmed) >= 29 && trimmed[14] == '-' {
		start, startErr := time.ParseInLocation("20060102150405", trimmed[:14], location)
		end, endErr := time.ParseInLocation("20060102150405", trimmed[15:29], location)
		if startErr == nil && endErr == nil {
			return start, end, nil
		}
	}
	start, err := timeFromFilename(filepath.Base(filename), location)
	if err != nil {
		return time.Time{}, time.Time{}, errors.New("recording has no valid embedded or filename timestamp")
	}
	end := start
	if endingValue != "" {
		if parsed, parseErr := time.ParseInLocation("20060102150405", endingValue, location); parseErr == nil {
			end = parsed
		}
	}
	return start, end, nil
}

func timeFromFilename(name string, location *time.Location) (time.Time, error) {
	if match := timestampFirstName.FindStringSubmatch(name); len(match) == 2 {
		return time.ParseInLocation("01-02-06 15-04-05", match[1], location)
	}
	if match := channelFirstName.FindStringSubmatch(name); len(match) == 2 {
		return time.ParseInLocation("20060102_15-04-05", match[1], location)
	}
	return time.Time{}, errors.New("unsupported filename timestamp")
}

func conventionalID(prefix, frequency, tone, channel string) string {
	parts := []string{strings.TrimSpace(prefix)}
	if value := stableIdentifier(frequency); value != "" {
		parts = append(parts, value)
	}
	if value := stableIdentifier(tone); value != "" {
		parts = append(parts, value)
	}
	if len(parts) == 1 {
		if value := stableIdentifier(channel); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, "-")
}

func stableIdentifier(value string) string {
	value = strings.Trim(identifierCleaner.ReplaceAllString(strings.TrimSpace(value), "-"), "-._")
	return strings.ToUpper(value)
}

func proScanNotes(metadata proScanMetadata) string {
	items := make([]string, 0, 5)
	for _, item := range []struct{ label, value string }{
		{"Tone", metadata.Tone}, {"RSSI", metadata.RSSI}, {"Service", metadata.ServiceType},
		{"Digital", metadata.DigitalStatus}, {"DMR slot", metadata.DMRSlot},
	} {
		if item.value != "" {
			items = append(items, item.label+": "+item.value)
		}
	}
	return strings.Join(items, "; ")
}

func syncSafeInt(value []byte) int {
	if len(value) != 4 {
		return 0
	}
	return int(value[0]&0x7f)<<21 | int(value[1]&0x7f)<<14 | int(value[2]&0x7f)<<7 | int(value[3]&0x7f)
}

func standardFrameID(value []byte) bool {
	if len(value) != 4 {
		return false
	}
	for _, item := range value {
		if !(item >= 'A' && item <= 'Z') && !(item >= '0' && item <= '9') {
			return false
		}
	}
	return true
}

func decodeID3Text(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	body := payload[1:]
	switch payload[0] {
	case 0, 3:
		return strings.TrimRight(string(body), "\x00")
	default:
		// ProScan samples use ISO-8859-1. Keep unsupported encodings visible
		// rather than silently inventing metadata.
		return strings.TrimRight(string(body), "\x00")
	}
}

func splitPipe(value string) []string { return strings.Split(strings.TrimRight(value, "\x00"), "|") }

func mp3Duration(audio []byte, searchStart int) (time.Duration, error) {
	position, header, ok := findMP3Frame(audio, searchStart)
	if !ok {
		return 0, errors.New("MP3 audio frame not found")
	}
	frames := 0
	for position+4 <= len(audio) {
		current, valid := decodeMP3Header(audio[position:])
		if !valid || current.SampleRate != header.SampleRate || current.Samples != header.Samples || position+current.Length > len(audio) {
			break
		}
		frames++
		position += current.Length
	}
	if frames == 0 {
		return 0, errors.New("MP3 contains no complete frames")
	}
	seconds := float64(frames*header.Samples) / float64(header.SampleRate)
	return time.Duration(seconds * float64(time.Second)), nil
}

type mp3Header struct {
	Length, SampleRate, Samples int
}

func findMP3Frame(audio []byte, start int) (int, mp3Header, bool) {
	if start < 0 || start >= len(audio) {
		start = 0
	}
	for position := start; position+4 <= len(audio); position++ {
		if header, ok := decodeMP3Header(audio[position:]); ok && position+header.Length <= len(audio) {
			return position, header, true
		}
	}
	return 0, mp3Header{}, false
}

func decodeMP3Header(audio []byte) (mp3Header, bool) {
	if len(audio) < 4 || audio[0] != 0xff || audio[1]&0xe0 != 0xe0 {
		return mp3Header{}, false
	}
	header := uint32(audio[0])<<24 | uint32(audio[1])<<16 | uint32(audio[2])<<8 | uint32(audio[3])
	versionBits := int((header >> 19) & 3)
	layerBits := int((header >> 17) & 3)
	bitrateIndex := int((header >> 12) & 15)
	sampleIndex := int((header >> 10) & 3)
	if versionBits == 1 || layerBits != 1 || bitrateIndex == 0 || bitrateIndex == 15 || sampleIndex == 3 {
		return mp3Header{}, false
	}
	v1Rates := []int{0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320}
	v2Rates := []int{0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160}
	sampleRates := map[int][]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}
	isV1 := versionBits == 3
	bitrate := v2Rates[bitrateIndex]
	coefficient, samples := 72, 576
	if isV1 {
		bitrate = v1Rates[bitrateIndex]
		coefficient, samples = 144, 1152
	}
	sampleRate := sampleRates[versionBits][sampleIndex]
	padding := int((header >> 9) & 1)
	length := coefficient*bitrate*1000/sampleRate + padding
	if length < 4 {
		return mp3Header{}, false
	}
	return mp3Header{Length: length, SampleRate: sampleRate, Samples: samples}, true
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}
