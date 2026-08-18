/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package handler

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"

	"interview-agent/internal/agent"
	"interview-agent/internal/auth"
	"interview-agent/internal/mcp"
	"interview-agent/internal/memory"
	"interview-agent/internal/rag"
	"interview-agent/internal/skill"
	"interview-agent/internal/speech"

	"github.com/cloudwego/eino/components/model"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ServerConfig Web 服务配置
type ServerConfig struct {
	ChatModel              model.ChatModel
	CombinedStore          memory.Store
	MilvusStore            *rag.MilvusStore   // Milvus 向量存储（题库读写+按用户隔离）
	BM25Manager            *rag.BM25Manager   // BM25 按用户管理
	RedisStore             *memory.RedisStore // Redis（文件 hash 判重）
	MySQLStore             *memory.MySQLStore
	ChatAgent              *agent.ChatAgent
	Router                 *agent.IntentRouter
	SkillRegistry          *skill.SkillRegistry // Skill 技能注册中心
	GitHubSearcher         *mcp.GitHubSearcher
	AuthService            *auth.Service   // 认证服务
	RerankerType           string          // 重排策略：cross-encoder（默认）/ llm / none
	RerankModel            string          // cross-encoder 重排模型（默认 gte-rerank-v2）
	APIKey                 string          // DashScope API Key（cross-encoder rerank 调用用）
	SpeechService          *speech.Service // 语音能力（nil 时 capabilities 返回 disabled）
	SpeechLogger           speech.EventLogger
	SpeechProvider         string
	SpeechTTSModel         string
	SpeechASRRealtimeModel string
	SpeechASRFallbackModel string
	AllowedOrigins         []string // HTTP API 允许的浏览器 Origin
	speechQuestions        *activeQuestionRegistry
}

// Server Web 服务器
type Server struct {
	cfg             *ServerConfig
	authHandler     *auth.Handler
	speechQuestions *activeQuestionRegistry
}

// NewServer 创建 Web 服务器
func NewServer(cfg *ServerConfig) *Server {
	questions := cfg.speechQuestions
	if questions == nil {
		questions = newActiveQuestionRegistry()
		cfg.speechQuestions = questions
	}
	s := &Server{cfg: cfg, speechQuestions: questions}
	if cfg.AuthService != nil {
		s.authHandler = auth.NewHandler(cfg.AuthService)
	}
	return s
}

// Start 启动 HTTP 服务器
func (s *Server) Start(addr string) error {
	log.Printf("[Web] 服务器启动: %s", addr)
	return http.ListenAndServe(addr, s.Handler())
}

// Handler 返回独立路由器，避免全局 DefaultServeMux 污染测试或重复启动。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// 认证 API
	if s.authHandler != nil {
		mux.HandleFunc("/api/register", corsMiddleware(s.authHandler.HandleRegister))
		mux.HandleFunc("/api/login", corsMiddleware(s.authHandler.HandleLogin))
	}

	mux.HandleFunc("/api/speech/capabilities", speechCORSMiddleware(s.cfg.AllowedOrigins, s.handleSpeechCapabilities))
	mux.HandleFunc("/api/speech/tts", speechCORSMiddleware(s.cfg.AllowedOrigins, s.handleTTS))
	mux.HandleFunc("/ws/speech/asr", s.handleASRWebSocket)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte("ok")); err != nil {
			return
		}
	})
	return mux
}

func (s *Server) authenticateWebSocketRequest(r *http.Request) (string, error) {
	if s.cfg.AuthService == nil {
		return "default_user", nil
	}
	token := r.URL.Query().Get("token")
	if token == "" {
		return "", errors.New("missing websocket token")
	}
	return s.cfg.AuthService.ValidateToken(token)
}

func (s *Server) authenticateHTTPRequest(r *http.Request) (string, error) {
	if s.cfg.AuthService == nil {
		return "default_user", nil
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errors.New("missing bearer token")
	}
	return s.cfg.AuthService.ValidateToken(parts[1])
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// JWT 鉴权：从 URL query 提取 token 并验证
	userID, err := s.authenticateWebSocketRequest(r)
	if err != nil {
		http.Error(w, "未授权：token 无效或缺失", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[WebSocket] 升级失败: %v", err)
		return
	}
	log.Printf("[WebSocket] 新连接，用户: %s", userID)

	session := NewWSSession(conn, s.cfg, userID)
	go session.Run()
}

// corsMiddleware 处理跨域请求（前端开发模式需要）
// corsMiddleware 保留现有认证 API 的跨域行为，避免语音关闭时改变旧链路。
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next(w, r)
	}
}

func speechCORSMiddleware(allowedOrigins []string, next http.HandlerFunc) http.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}

	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; !ok {
				http.Error(w, "Forbidden Origin", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}
