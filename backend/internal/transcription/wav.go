// Package transcription contains shared helpers for the transcription worker and web admin.
package transcription

import (
	"bytes"
	"encoding/binary"
)

// SyntheticWAV returns a tiny, valid, silent mono WAV suitable for provider connectivity tests.
func SyntheticWAV() ([]byte, error) {
	const sampleRate = 16000
	const seconds = 0.1
	numSamples := int(sampleRate * seconds)
	data := make([]byte, numSamples*2)
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, uint32(36+len(data)))
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16)) // subchunk1size
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // audio format PCM
	binary.Write(&buf, binary.LittleEndian, uint16(1))  // num channels
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(sampleRate*2)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(2))            // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))           // bits per sample
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(data)))
	buf.Write(data)
	return buf.Bytes(), nil
}
