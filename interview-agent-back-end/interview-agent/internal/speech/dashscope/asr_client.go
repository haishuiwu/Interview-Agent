package dashscope

import (
	"context"
	"fmt"

	"interview-agent/internal/speech"
)

// ASRClient combines the realtime and bounded HTTP adapters behind the
// provider-neutral speech.Transcriber contract.
type ASRClient struct {
	realtime *RealtimeASRClient
	fallback *FallbackASRClient
}

func NewASRClient(realtime *RealtimeASRClient, fallback *FallbackASRClient) (*ASRClient, error) {
	if realtime == nil || fallback == nil {
		return nil, fmt.Errorf("dashscope ASR: realtime and fallback clients are required")
	}
	return &ASRClient{realtime: realtime, fallback: fallback}, nil
}

func (c *ASRClient) Start(
	ctx context.Context,
	cfg speech.ASRConfig,
	onEvent func(speech.TranscriptEvent),
) (speech.ASRStream, error) {
	return c.realtime.Start(ctx, cfg, onEvent)
}

func (c *ASRClient) TranscribeBuffered(ctx context.Context, wav []byte) (speech.TranscriptEvent, error) {
	return c.fallback.TranscribeBuffered(ctx, wav)
}

var _ speech.Transcriber = (*ASRClient)(nil)
