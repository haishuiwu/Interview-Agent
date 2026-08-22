/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrUserExists       = errors.New("用户名已存在")
	ErrInvalidCredential = errors.New("用户名或密码错误")
	ErrInvalidInput     = errors.New("参数不合法")
)

// Service 认证服务
type Service struct {
	db        *sql.DB
	jwtSecret []byte
}

// NewService 创建认证服务，自动建表
func NewService(db *sql.DB, jwtSecret string) (*Service, error) {
	s := &Service{
		db:        db,
		jwtSecret: []byte(jwtSecret),
	}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("auth: migrate: %w", err)
	}
	log.Println("[Auth] 用户表就绪")
	return s, nil
}

func (s *Service) migrate() error {
	query := `CREATE TABLE IF NOT EXISTS users (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		username VARCHAR(64) NOT NULL UNIQUE,
		password_hash VARCHAR(256) NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4`
	_, err := s.db.Exec(query)
	return err
}

// Register 注册新用户，返回 JWT
func (s *Service) Register(ctx context.Context, username, password string) (string, error) {
	if err := validateInput(username, password); err != nil {
		return "", err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("auth: bcrypt: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		"INSERT INTO users (username, password_hash) VALUES (?, ?)",
		username, string(hash),
	)
	if err != nil {
		// MySQL duplicate entry
		if isDuplicateEntry(err) {
			return "", ErrUserExists
		}
		return "", fmt.Errorf("auth: insert user: %w", err)
	}

	return s.generateToken(username)
}

// Login 登录验证，返回 JWT
func (s *Service) Login(ctx context.Context, username, password string) (string, error) {
	var hash string
	err := s.db.QueryRowContext(ctx,
		"SELECT password_hash FROM users WHERE username = ?",
		username,
	).Scan(&hash)
	if err == sql.ErrNoRows {
		return "", ErrInvalidCredential
	}
	if err != nil {
		return "", fmt.Errorf("auth: query user: %w", err)
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return "", ErrInvalidCredential
	}

	return s.generateToken(username)
}

// ValidateToken 验证 JWT，返回 username
func (s *Service) ValidateToken(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.jwtSecret, nil
	})
	if err != nil {
		return "", fmt.Errorf("auth: invalid token: %w", err)
	}

	sub, err := token.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", fmt.Errorf("auth: missing subject in token")
	}

	return sub, nil
}

func (s *Service) generateToken(username string) (string, error) {
	claims := jwt.RegisteredClaims{
		Subject:   username,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(7 * 24 * time.Hour)),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.jwtSecret)
}

func validateInput(username, password string) error {
	uLen := utf8.RuneCountInString(username)
	if uLen < 2 || uLen > 32 {
		return fmt.Errorf("%w: 用户名长度需在 2-32 字符之间", ErrInvalidInput)
	}
	pLen := utf8.RuneCountInString(password)
	if pLen < 6 || pLen > 64 {
		return fmt.Errorf("%w: 密码长度需在 6-64 字符之间", ErrInvalidInput)
	}
	return nil
}

func isDuplicateEntry(err error) bool {
	return err != nil && (contains(err.Error(), "Duplicate entry") || contains(err.Error(), "duplicate key"))
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
