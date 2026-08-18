package speech

// PCMBuffer stores signed 16-bit PCM up to a fixed byte limit. It is owned by
// one ASR connection, so callers serialize access through the connection state
// machine rather than paying for an additional lock per audio frame.
type PCMBuffer struct {
	data     []byte
	maxBytes int
	released bool
}

func NewPCMBuffer(maxBytes int) (*PCMBuffer, error) {
	if maxBytes <= 0 || maxBytes%2 != 0 {
		return nil, ErrInvalidRequest
	}
	initialCapacity := maxBytes
	if initialCapacity > 64*1024 {
		initialCapacity = 64 * 1024
	}
	return &PCMBuffer{data: make([]byte, 0, initialCapacity), maxBytes: maxBytes}, nil
}

// Append retains at most the configured limit. full is true once callers must
// stop accepting browser audio and move to finalization.
func (b *PCMBuffer) Append(frame []byte) (full bool, err error) {
	if b == nil || b.released {
		return false, ErrClientCancelled
	}
	if len(frame) == 0 || len(frame)%2 != 0 {
		return false, ErrInvalidRequest
	}
	remaining := b.maxBytes - len(b.data)
	if remaining <= 0 {
		return true, nil
	}
	if len(frame) > remaining {
		frame = frame[:remaining]
	}
	b.data = append(b.data, frame...)
	return len(b.data) == b.maxBytes, nil
}

func (b *PCMBuffer) Len() int {
	if b == nil {
		return 0
	}
	return len(b.data)
}

// Bytes returns the connection-owned backing slice. Callers must not retain it
// after Release and must not mutate it while the session is active.
func (b *PCMBuffer) Bytes() []byte {
	if b == nil || b.released {
		return nil
	}
	return b.data
}

// Release removes the only buffer reference so audio can be reclaimed as soon
// as the ASR connection reaches a terminal state.
func (b *PCMBuffer) Release() {
	if b == nil {
		return
	}
	b.data = nil
	b.released = true
}
