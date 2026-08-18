package speech

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestJSONEventLoggerWritesBoundedPrivacySafeMetadata(t *testing.T) {
	var output bytes.Buffer
	logger := NewJSONEventLogger(&output, "test-hash-key")
	logger.LogSpeechEvent(context.Background(), Event{
		Name: EventASRCompleted, RequestID: "request-1", QuestionID: "question-1",
		UserID: "private-user@example.com", Provider: "dashscope", Model: "qwen3-asr-flash",
		ProviderRequestID: "provider-1", DurationMS: 25, FirstResultMS: 10,
		AudioBytes: 640, TextChars: 8, Degraded: true,
	})

	line := strings.TrimSpace(output.String())
	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("decode JSON log %q: %v", line, err)
	}
	if record["event"] != EventASRCompleted || record["request_id"] != "request-1" || record["degraded"] != true {
		t.Fatalf("unexpected speech log: %#v", record)
	}
	userHash, ok := record["user_id_hash"].(string)
	if !ok || len(userHash) != 32 || userHash == "private-user@example.com" {
		t.Fatalf("unsafe user hash: %#v", record["user_id_hash"])
	}
	for _, forbidden := range []string{"private-user@example.com", "DASHSCOPE_API_KEY", "Bearer ", "data:audio", "完整转写"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("speech log contains forbidden value %q: %s", forbidden, line)
		}
	}
}

func TestJSONEventLoggerSupportsConcurrentEvents(t *testing.T) {
	var output lockedBuffer
	logger := NewJSONEventLogger(&output, "test-hash-key")
	var wait sync.WaitGroup
	for i := 0; i < 100; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			logger.LogSpeechEvent(context.Background(), Event{Name: EventASRStarted, UserID: "user"})
		}()
	}
	wait.Wait()

	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	count := 0
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid concurrent JSON log: %v", err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan logs: %v", err)
	}
	if count != 100 {
		t.Fatalf("log lines = %d, want 100", count)
	}
}

func TestSanitizeLogValuePreservesUTF8AndDropsControls(t *testing.T) {
	got := sanitizeLogValue("  你好\n世界  ", 7)
	if got != "你好" || !utf8.ValidString(got) {
		t.Fatalf("sanitizeLogValue = %q, want valid UTF-8 %q", got, "你好")
	}
	if got := sanitizeLogValue("value", 0); got != "" {
		t.Fatalf("sanitizeLogValue with zero limit = %q, want empty", got)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(data)
}

func (b *lockedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.b.Bytes()...)
}
