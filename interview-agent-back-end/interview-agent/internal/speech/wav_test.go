package speech

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestPCMToWAVBuildsMono16BitHeaderAndPreservesPayload(t *testing.T) {
	pcm := []byte{0x01, 0x02, 0x03, 0x04}
	wav, err := PCMToWAV(pcm, InputSampleRate, 1)
	if err != nil {
		t.Fatalf("PCMToWAV: %v", err)
	}
	if len(wav) != 44+len(pcm) {
		t.Fatalf("WAV length = %d, want %d", len(wav), 44+len(pcm))
	}
	if !IsWAV(wav) {
		t.Fatal("PCMToWAV result is not recognized as WAV")
	}
	if got := binary.LittleEndian.Uint32(wav[4:8]); got != uint32(36+len(pcm)) {
		t.Fatalf("RIFF size = %d", got)
	}
	if got := binary.LittleEndian.Uint16(wav[20:22]); got != 1 {
		t.Fatalf("audio format = %d, want PCM", got)
	}
	if got := binary.LittleEndian.Uint16(wav[22:24]); got != 1 {
		t.Fatalf("channels = %d", got)
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != InputSampleRate {
		t.Fatalf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(wav[28:32]); got != InputSampleRate*2 {
		t.Fatalf("byte rate = %d", got)
	}
	if got := binary.LittleEndian.Uint16(wav[32:34]); got != 2 {
		t.Fatalf("block align = %d", got)
	}
	if got := binary.LittleEndian.Uint16(wav[34:36]); got != 16 {
		t.Fatalf("bits per sample = %d", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != uint32(len(pcm)) {
		t.Fatalf("data size = %d", got)
	}
	if !bytes.Equal(wav[44:], pcm) {
		t.Fatalf("PCM payload changed: %v", wav[44:])
	}
}

func TestPCMToWAVRejectsInvalidPCM(t *testing.T) {
	for _, test := range []struct {
		name       string
		pcm        []byte
		sampleRate int
		channels   int
	}{
		{name: "empty", sampleRate: InputSampleRate, channels: 1},
		{name: "odd bytes", pcm: []byte{1}, sampleRate: InputSampleRate, channels: 1},
		{name: "sample rate", pcm: []byte{1, 2}, sampleRate: 0, channels: 1},
		{name: "channels", pcm: []byte{1, 2}, sampleRate: InputSampleRate, channels: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := PCMToWAV(test.pcm, test.sampleRate, test.channels)
			if !errors.Is(err, ErrInvalidRequest) && !errors.Is(err, ErrEmptyAudio) {
				t.Fatalf("PCMToWAV error = %v", err)
			}
		})
	}
}
