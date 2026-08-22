package speech

import (
	"bytes"
	"encoding/binary"
	"math"
)

const pcmWAVHeaderBytes = 44

// PCMToWAV wraps signed 16-bit little-endian PCM in a canonical RIFF/WAVE
// header. The input payload is copied once into the returned bounded buffer.
func PCMToWAV(pcm []byte, sampleRate, channels int) ([]byte, error) {
	if len(pcm) == 0 {
		return nil, ErrEmptyAudio
	}
	if sampleRate <= 0 || channels <= 0 || channels > math.MaxUint16 {
		return nil, ErrInvalidRequest
	}
	blockAlign := channels * 2
	if len(pcm)%blockAlign != 0 || len(pcm) > math.MaxUint32-36 {
		return nil, ErrInvalidRequest
	}
	byteRate := uint64(sampleRate) * uint64(blockAlign)
	if byteRate > math.MaxUint32 {
		return nil, ErrInvalidRequest
	}

	wav := make([]byte, pcmWAVHeaderBytes+len(pcm))
	copy(wav[0:4], "RIFF")
	binary.LittleEndian.PutUint32(wav[4:8], uint32(36+len(pcm)))
	copy(wav[8:12], "WAVE")
	copy(wav[12:16], "fmt ")
	binary.LittleEndian.PutUint32(wav[16:20], 16)
	binary.LittleEndian.PutUint16(wav[20:22], 1)
	binary.LittleEndian.PutUint16(wav[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(wav[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(wav[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(wav[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(wav[34:36], 16)
	copy(wav[36:40], "data")
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(pcm)))
	copy(wav[44:], pcm)
	return wav, nil
}

// IsWAV performs the minimal container signature check required before
// proxying provider bytes as audio/wav.
func IsWAV(data []byte) bool {
	return len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
}
