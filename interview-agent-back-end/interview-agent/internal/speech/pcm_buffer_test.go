package speech

import (
	"errors"
	"testing"
)

func TestPCMBufferIsBoundedAndReleasable(t *testing.T) {
	buffer, err := NewPCMBuffer(8)
	if err != nil {
		t.Fatalf("NewPCMBuffer: %v", err)
	}
	full, err := buffer.Append([]byte{1, 2, 3, 4, 5, 6})
	if err != nil || full {
		t.Fatalf("first Append = full:%v err:%v", full, err)
	}
	full, err = buffer.Append([]byte{7, 8, 9, 10})
	if err != nil || !full {
		t.Fatalf("second Append = full:%v err:%v", full, err)
	}
	if got := buffer.Len(); got != 8 {
		t.Fatalf("Len = %d, want 8", got)
	}
	if got := buffer.Bytes(); len(got) != 8 || got[7] != 8 {
		t.Fatalf("Bytes = %v", got)
	}
	buffer.Release()
	if buffer.Len() != 0 || buffer.Bytes() != nil {
		t.Fatalf("buffer retained data after Release: len=%d bytes=%v", buffer.Len(), buffer.Bytes())
	}
}

func TestPCMBufferRejectsInvalidFrames(t *testing.T) {
	if _, err := NewPCMBuffer(0); err == nil {
		t.Fatal("NewPCMBuffer accepted zero capacity")
	}
	buffer, err := NewPCMBuffer(8)
	if err != nil {
		t.Fatalf("NewPCMBuffer: %v", err)
	}
	if _, err := buffer.Append([]byte{1}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("odd PCM error = %v, want ErrInvalidRequest", err)
	}
	buffer.Release()
	if _, err := buffer.Append([]byte{1, 2}); !errors.Is(err, ErrClientCancelled) {
		t.Fatalf("released buffer error = %v, want ErrClientCancelled", err)
	}
}
