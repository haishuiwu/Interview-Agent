/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

// Package config 管理 InterviewAgent 的全局配置
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config 全局配置
type Config struct {
	// 大模型配置
	LLM LLMConfig
	// 向量数据库
	Milvus MilvusConfig
	// Redis
	Redis RedisConfig
	// MySQL
	MySQL MySQLConfig
	// GitHub
	GitHub GitHubConfig
	// JWT
	JWT JWTConfig
	// 语音能力
	Speech SpeechConfig
}

// SpeechConfig 语音功能配置。APIKey 默认复用 DASHSCOPE_API_KEY。
type SpeechConfig struct {
	Enabled            bool
	TTSEnabled         bool
	ASREnabled         bool
	APIKey             string
	TTSModel           string
	TTSVoice           string
	TTSLanguage        string
	TTSBaseURL         string
	ASRRealtimeModel   string
	ASRFallbackModel   string
	ASRRealtimeURL     string
	ASRFallbackBaseURL string
	MaxTTSChars        int
	MaxTTSAudioBytes   int64
	MaxAnswerSeconds   int
	TTSConcurrency     int
	ASRConcurrency     int
	TTSTimeout         time.Duration
	ASRConnectTimeout  time.Duration
	ASRFinalTimeout    time.Duration
	ASRFallbackTimeout time.Duration
	AllowedOrigins     []string
}

// JWTConfig JWT 认证配置
type JWTConfig struct {
	Secret string // JWT 签名密钥
}

// GitHubConfig GitHub MCP 配置
type GitHubConfig struct {
	Token string // GitHub Personal Access Token（可选，用于复习计划推荐开源项目）
}

// LLMConfig 大模型相关配置
type LLMConfig struct {
	APIKey         string // DashScope API Key
	BaseURL        string // API Base URL
	Model          string // 默认模型（qwen-plus）
	EmbeddingModel string // Embedding 模型
	RerankerType   string // 重排策略：cross-encoder（默认）/ llm / none
	RerankModel    string // cross-encoder 使用的重排模型（默认 gte-rerank-v2）
}

// MilvusConfig Milvus 向量数据库配置
type MilvusConfig struct {
	Addr string // 连接地址
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

// MySQLConfig MySQL 配置
type MySQLConfig struct {
	DSN string
}

// Load 从环境变量加载配置（自动尝试读取 .env 文件）
func Load() (*Config, error) {
	// 尝试加载 .env 文件，不存在也不报错
	_ = godotenv.Load()

	apiKey := os.Getenv("DASHSCOPE_API_KEY")
	speechCfg, err := loadSpeechConfig(apiKey)
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		return nil, fmt.Errorf("config: DASHSCOPE_API_KEY is required")
	}

	redisDB := 0
	if v := os.Getenv("REDIS_DB"); v != "" {
		var err error
		redisDB, err = strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("config: invalid REDIS_DB: %w", err)
		}
	}

	return &Config{
		LLM: LLMConfig{
			APIKey:         apiKey,
			BaseURL:        getEnvDefault("LLM_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
			Model:          getEnvDefault("LLM_MODEL", "qwen-plus"),
			EmbeddingModel: getEnvDefault("EMBEDDING_MODEL", "text-embedding-v3"),
			RerankerType:   getEnvDefault("RERANKER_TYPE", "cross-encoder"),
			RerankModel:    getEnvDefault("RERANK_MODEL", "gte-rerank-v2"),
		},
		Milvus: MilvusConfig{
			Addr: getEnvDefault("MILVUS_ADDR", "localhost:19530"),
		},
		Redis: RedisConfig{
			Addr:     getEnvDefault("REDIS_ADDR", "localhost:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       redisDB,
		},
		MySQL: MySQLConfig{
			DSN: getEnvDefault("MYSQL_DSN", "root:interview@tcp(localhost:3306)/interview_agent?charset=utf8mb4&parseTime=True&loc=Local"),
		},
		GitHub: GitHubConfig{
			Token: os.Getenv("GITHUB_TOKEN"),
		},
		JWT: JWTConfig{
			Secret: getEnvDefault("JWT_SECRET", "interview-agent-default-secret"),
		},
		Speech: speechCfg,
	}, nil
}

func loadSpeechConfig(apiKey string) (SpeechConfig, error) {
	enabled, err := getEnvBool("SPEECH_ENABLED", false)
	if err != nil {
		return SpeechConfig{}, err
	}
	ttsEnabled, err := getEnvBool("SPEECH_TTS_ENABLED", true)
	if err != nil {
		return SpeechConfig{}, err
	}
	asrEnabled, err := getEnvBool("SPEECH_ASR_ENABLED", true)
	if err != nil {
		return SpeechConfig{}, err
	}

	maxTTSChars, err := getEnvPositiveInt("SPEECH_MAX_TTS_CHARS", 500)
	if err != nil {
		return SpeechConfig{}, err
	}
	maxTTSAudioBytes, err := getEnvPositiveInt("SPEECH_MAX_TTS_AUDIO_BYTES", 10*1024*1024)
	if err != nil {
		return SpeechConfig{}, err
	}
	maxAnswerSeconds, err := getEnvPositiveInt("SPEECH_MAX_ANSWER_SECONDS", 180)
	if err != nil {
		return SpeechConfig{}, err
	}
	ttsConcurrency, err := getEnvPositiveInt("SPEECH_MAX_CONCURRENT_TTS", 20)
	if err != nil {
		return SpeechConfig{}, err
	}
	asrConcurrency, err := getEnvPositiveInt("SPEECH_MAX_CONCURRENT_ASR", 20)
	if err != nil {
		return SpeechConfig{}, err
	}
	ttsTimeout, err := getEnvPositiveDurationSeconds("SPEECH_TTS_TIMEOUT_SECONDS", 15)
	if err != nil {
		return SpeechConfig{}, err
	}
	asrConnectTimeout, err := getEnvPositiveDurationSeconds("SPEECH_ASR_CONNECT_TIMEOUT_SECONDS", 5)
	if err != nil {
		return SpeechConfig{}, err
	}
	asrFinalTimeout, err := getEnvPositiveDurationSeconds("SPEECH_ASR_FINAL_TIMEOUT_SECONDS", 8)
	if err != nil {
		return SpeechConfig{}, err
	}
	asrFallbackTimeout, err := getEnvPositiveDurationSeconds("SPEECH_ASR_FALLBACK_TIMEOUT_SECONDS", 45)
	if err != nil {
		return SpeechConfig{}, err
	}
	allowedOrigins, err := parseAllowedOrigins(getEnvDefault("WEB_ALLOWED_ORIGINS", "http://localhost:5173"))
	if err != nil {
		return SpeechConfig{}, err
	}

	cfg := SpeechConfig{
		Enabled:            enabled,
		TTSEnabled:         ttsEnabled,
		ASREnabled:         asrEnabled,
		APIKey:             apiKey,
		TTSModel:           getEnvDefault("DASHSCOPE_TTS_MODEL", "qwen3-tts-flash"),
		TTSVoice:           getEnvDefault("DASHSCOPE_TTS_VOICE", "Cherry"),
		TTSLanguage:        getEnvDefault("DASHSCOPE_TTS_LANGUAGE", "Auto"),
		TTSBaseURL:         getEnvDefault("DASHSCOPE_TTS_BASE_URL", "https://dashscope.aliyuncs.com/api/v1"),
		ASRRealtimeModel:   getEnvDefault("DASHSCOPE_ASR_REALTIME_MODEL", "qwen3-asr-flash-realtime"),
		ASRFallbackModel:   getEnvDefault("DASHSCOPE_ASR_FALLBACK_MODEL", "qwen3-asr-flash"),
		ASRRealtimeURL:     getEnvDefault("DASHSCOPE_ASR_REALTIME_URL", "wss://dashscope.aliyuncs.com/api-ws/v1/realtime"),
		ASRFallbackBaseURL: getEnvDefault("DASHSCOPE_ASR_FALLBACK_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
		MaxTTSChars:        maxTTSChars,
		MaxTTSAudioBytes:   int64(maxTTSAudioBytes),
		MaxAnswerSeconds:   maxAnswerSeconds,
		TTSConcurrency:     ttsConcurrency,
		ASRConcurrency:     asrConcurrency,
		TTSTimeout:         ttsTimeout,
		ASRConnectTimeout:  asrConnectTimeout,
		ASRFinalTimeout:    asrFinalTimeout,
		ASRFallbackTimeout: asrFallbackTimeout,
		AllowedOrigins:     allowedOrigins,
	}

	if !cfg.Enabled {
		return cfg, nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" {
		return SpeechConfig{}, fmt.Errorf("config: DASHSCOPE_API_KEY is required when SPEECH_ENABLED=true")
	}
	if cfg.TTSEnabled {
		if err := validateNonEmpty("DASHSCOPE_TTS_MODEL", cfg.TTSModel); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateNonEmpty("DASHSCOPE_TTS_VOICE", cfg.TTSVoice); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateNonEmpty("DASHSCOPE_TTS_LANGUAGE", cfg.TTSLanguage); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateEndpoint("DASHSCOPE_TTS_BASE_URL", cfg.TTSBaseURL, "http", "https"); err != nil {
			return SpeechConfig{}, err
		}
	}
	if cfg.ASREnabled {
		if err := validateNonEmpty("DASHSCOPE_ASR_REALTIME_MODEL", cfg.ASRRealtimeModel); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateNonEmpty("DASHSCOPE_ASR_FALLBACK_MODEL", cfg.ASRFallbackModel); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateEndpoint("DASHSCOPE_ASR_REALTIME_URL", cfg.ASRRealtimeURL, "ws", "wss"); err != nil {
			return SpeechConfig{}, err
		}
		if err := validateEndpoint("DASHSCOPE_ASR_FALLBACK_BASE_URL", cfg.ASRFallbackBaseURL, "http", "https"); err != nil {
			return SpeechConfig{}, err
		}
	}
	return cfg, nil
}

func getEnvBool(key string, defaultVal bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("config: invalid %s: %w", key, err)
	}
	return value, nil
}

func getEnvPositiveInt(key string, defaultVal int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return defaultVal, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("config: invalid %s: must be a positive integer", key)
	}
	return value, nil
}

func getEnvPositiveDurationSeconds(key string, defaultVal int) (time.Duration, error) {
	seconds, err := getEnvPositiveInt(key, defaultVal)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}

func parseAllowedOrigins(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	origins := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSuffix(strings.TrimSpace(item), "/")
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return nil, fmt.Errorf("config: invalid WEB_ALLOWED_ORIGINS entry %q", item)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		origins = append(origins, origin)
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("config: WEB_ALLOWED_ORIGINS must contain at least one origin")
	}
	return origins, nil
}

func validateNonEmpty(key, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("config: %s must not be empty", key)
	}
	return nil
}

func validateEndpoint(key, value string, allowedSchemes ...string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("config: invalid %s", key)
	}
	for _, scheme := range allowedSchemes {
		if parsed.Scheme == scheme {
			return nil
		}
	}
	return fmt.Errorf("config: invalid %s scheme %q", key, parsed.Scheme)
}

func getEnvDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
