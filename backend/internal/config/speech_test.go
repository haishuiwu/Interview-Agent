package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadSpeechConfigDefaultsDisabled(t *testing.T) {
	clearSpeechEnvironment(t)
	t.Setenv("DASHSCOPE_API_KEY", "shared-key")

	rootConfig, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := rootConfig.Speech
	if cfg.Enabled {
		t.Fatal("speech must be disabled by default")
	}
	if !cfg.TTSEnabled || !cfg.ASREnabled {
		t.Fatalf("provider feature defaults = tts:%v asr:%v, want true/true", cfg.TTSEnabled, cfg.ASREnabled)
	}
	if cfg.APIKey != "shared-key" {
		t.Fatal("speech API key did not reuse DASHSCOPE_API_KEY")
	}
	if cfg.MaxTTSChars != 500 || cfg.MaxTTSAudioBytes != 10*1024*1024 || cfg.MaxAnswerSeconds != 180 {
		t.Fatalf("unexpected size defaults: %+v", cfg)
	}
	if cfg.TTSTimeout != 15*time.Second || cfg.ASRConnectTimeout != 5*time.Second || cfg.ASRFinalTimeout != 8*time.Second || cfg.ASRFallbackTimeout != 45*time.Second {
		t.Fatalf("unexpected timeout defaults: %+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 1 || cfg.AllowedOrigins[0] != "http://localhost:5173" {
		t.Fatalf("allowed origins = %v", cfg.AllowedOrigins)
	}
}

func TestLoadSpeechConfigEnabledRequiresSharedAPIKey(t *testing.T) {
	clearSpeechEnvironment(t)
	t.Setenv("SPEECH_ENABLED", "true")

	_, err := loadSpeechConfig("")
	if err == nil || !strings.Contains(err.Error(), "DASHSCOPE_API_KEY") {
		t.Fatalf("loadSpeechConfig error = %v, want missing API key error", err)
	}
}

func TestLoadSpeechConfigRejectsInvalidPositiveValue(t *testing.T) {
	clearSpeechEnvironment(t)
	t.Setenv("SPEECH_MAX_CONCURRENT_TTS", "0")

	_, err := loadSpeechConfig("shared-key")
	if err == nil || !strings.Contains(err.Error(), "SPEECH_MAX_CONCURRENT_TTS") {
		t.Fatalf("loadSpeechConfig error = %v, want invalid concurrency error", err)
	}
}

func TestLoadSpeechConfigRejectsInvalidEnabledEndpoint(t *testing.T) {
	clearSpeechEnvironment(t)
	t.Setenv("SPEECH_ENABLED", "true")
	t.Setenv("DASHSCOPE_TTS_BASE_URL", "file:///tmp/audio")

	_, err := loadSpeechConfig("shared-key")
	if err == nil || !strings.Contains(err.Error(), "DASHSCOPE_TTS_BASE_URL") {
		t.Fatalf("loadSpeechConfig error = %v, want invalid TTS endpoint error", err)
	}
}

func TestLoadSpeechConfigRejectsWildcardOrOriginPath(t *testing.T) {
	for _, origin := range []string{"*", "https://app.example/path", "https://app.example?token=secret"} {
		t.Run(origin, func(t *testing.T) {
			clearSpeechEnvironment(t)
			t.Setenv("WEB_ALLOWED_ORIGINS", origin)
			_, err := loadSpeechConfig("shared-key")
			if err == nil || !strings.Contains(err.Error(), "WEB_ALLOWED_ORIGINS") {
				t.Fatalf("loadSpeechConfig error = %v, want invalid Origin", err)
			}
		})
	}
}

func TestLoadSpeechConfigOverrides(t *testing.T) {
	clearSpeechEnvironment(t)
	t.Setenv("SPEECH_ENABLED", "true")
	t.Setenv("SPEECH_ASR_ENABLED", "false")
	t.Setenv("SPEECH_MAX_TTS_CHARS", "800")
	t.Setenv("SPEECH_TTS_TIMEOUT_SECONDS", "9")
	t.Setenv("SPEECH_ASR_FALLBACK_TIMEOUT_SECONDS", "12")
	t.Setenv("WEB_ALLOWED_ORIGINS", "https://one.example, https://two.example/")

	cfg, err := loadSpeechConfig("shared-key")
	if err != nil {
		t.Fatalf("loadSpeechConfig: %v", err)
	}
	if !cfg.Enabled || !cfg.TTSEnabled || cfg.ASREnabled {
		t.Fatalf("unexpected feature flags: %+v", cfg)
	}
	if cfg.MaxTTSChars != 800 || cfg.TTSTimeout != 9*time.Second || cfg.ASRFallbackTimeout != 12*time.Second {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[1] != "https://two.example" {
		t.Fatalf("allowed origins = %v", cfg.AllowedOrigins)
	}
}

func clearSpeechEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"SPEECH_ENABLED",
		"SPEECH_TTS_ENABLED",
		"SPEECH_ASR_ENABLED",
		"DASHSCOPE_TTS_MODEL",
		"DASHSCOPE_TTS_VOICE",
		"DASHSCOPE_TTS_LANGUAGE",
		"DASHSCOPE_TTS_BASE_URL",
		"DASHSCOPE_ASR_REALTIME_MODEL",
		"DASHSCOPE_ASR_FALLBACK_MODEL",
		"DASHSCOPE_ASR_REALTIME_URL",
		"DASHSCOPE_ASR_FALLBACK_BASE_URL",
		"SPEECH_MAX_TTS_CHARS",
		"SPEECH_MAX_TTS_AUDIO_BYTES",
		"SPEECH_MAX_ANSWER_SECONDS",
		"SPEECH_MAX_CONCURRENT_TTS",
		"SPEECH_MAX_CONCURRENT_ASR",
		"SPEECH_TTS_TIMEOUT_SECONDS",
		"SPEECH_ASR_CONNECT_TIMEOUT_SECONDS",
		"SPEECH_ASR_FINAL_TIMEOUT_SECONDS",
		"SPEECH_ASR_FALLBACK_TIMEOUT_SECONDS",
		"WEB_ALLOWED_ORIGINS",
	} {
		t.Setenv(key, "")
	}
}
