/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package auth

import (
	"encoding/json"
	"errors"
	"net/http"
)

type authRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authResponse struct {
	Token    string `json:"token,omitempty"`
	Username string `json:"username,omitempty"`
	Error    string `json:"error,omitempty"`
}

// Handler HTTP 认证接口
type Handler struct {
	service *Service
}

// NewHandler 创建认证接口
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// HandleRegister 处理注册请求
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, authResponse{Error: "请求格式错误"})
		return
	}

	token, err := h.service.Register(r.Context(), req.Username, req.Password)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrUserExists) {
			code = http.StatusBadRequest
		} else if errors.Is(err, ErrInvalidInput) {
			code = http.StatusBadRequest
		}
		writeJSON(w, code, authResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token, Username: req.Username})
}

// HandleLogin 处理登录请求
func (h *Handler) HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	var req authRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, authResponse{Error: "请求格式错误"})
		return
	}

	token, err := h.service.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrInvalidCredential) {
			code = http.StatusUnauthorized
		}
		writeJSON(w, code, authResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, authResponse{Token: token, Username: req.Username})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
