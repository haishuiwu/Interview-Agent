/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// Store 持久化存储接口
type Store interface {
	SaveProfile(ctx context.Context, profile *UserProfile) error
	LoadProfile(ctx context.Context, userID string) (*UserProfile, error)
	SaveSession(ctx context.Context, sessionID string, data []byte, ttl time.Duration) error
	LoadSession(ctx context.Context, sessionID string) ([]byte, error)
}

// RedisStore 基于 Redis 的会话缓存 + 用户画像存储
type RedisStore struct {
	client *redis.Client
}

// NewRedisStore 创建 Redis 存储
func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

func profileKey(userID string) string {
	return fmt.Sprintf("interview:profile:%s", userID)
}

func sessionKey(sessionID string) string {
	return fmt.Sprintf("interview:session:%s", sessionID)
}

func fileHashKey(userID, filename string) string {
	return fmt.Sprintf("interview:file_hash:%s:%s", userID, filename)
}

// SaveProfile 保存用户画像到 Redis
func (s *RedisStore) SaveProfile(ctx context.Context, profile *UserProfile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return fmt.Errorf("redis_store: marshal profile: %w", err)
	}
	return s.client.Set(ctx, profileKey(profile.UserID), data, 0).Err()
}

// LoadProfile 从 Redis 加载用户画像
func (s *RedisStore) LoadProfile(ctx context.Context, userID string) (*UserProfile, error) {
	data, err := s.client.Get(ctx, profileKey(userID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis_store: load profile: %w", err)
	}

	profile := &UserProfile{}
	if err := json.Unmarshal(data, profile); err != nil {
		return nil, fmt.Errorf("redis_store: unmarshal profile: %w", err)
	}
	return profile, nil
}

// SaveSession 保存会话数据到 Redis
func (s *RedisStore) SaveSession(ctx context.Context, sessionID string, data []byte, ttl time.Duration) error {
	return s.client.Set(ctx, sessionKey(sessionID), data, ttl).Err()
}

// LoadSession 从 Redis 加载会话数据
func (s *RedisStore) LoadSession(ctx context.Context, sessionID string) ([]byte, error) {
	data, err := s.client.Get(ctx, sessionKey(sessionID)).Bytes()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis_store: load session: %w", err)
	}
	return data, nil
}

// GetFileHash 获取用户某文件的 content hash（用于判断内容是否变化）
func (s *RedisStore) GetFileHash(ctx context.Context, userID, filename string) (string, error) {
	hash, err := s.client.Get(ctx, fileHashKey(userID, filename)).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("redis_store: get file hash: %w", err)
	}
	return hash, nil
}

// SaveFileHash 保存用户某文件的 content hash
func (s *RedisStore) SaveFileHash(ctx context.Context, userID, filename, hash string) error {
	return s.client.Set(ctx, fileHashKey(userID, filename), hash, 0).Err()
}

// MemoryStore 内存存储（用于开发和测试）
type MemoryStore struct {
	mu       sync.RWMutex
	profiles map[string][]byte
	sessions map[string][]byte
}

// NewMemoryStore 创建内存存储
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		profiles: make(map[string][]byte),
		sessions: make(map[string][]byte),
	}
}

// SaveProfile 保存用户画像到内存
func (s *MemoryStore) SaveProfile(_ context.Context, profile *UserProfile) error {
	data, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.profiles[profile.UserID] = data
	s.mu.Unlock()
	return nil
}

// LoadProfile 从内存加载用户画像
func (s *MemoryStore) LoadProfile(_ context.Context, userID string) (*UserProfile, error) {
	s.mu.RLock()
	data, ok := s.profiles[userID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	profile := &UserProfile{}
	return profile, json.Unmarshal(data, profile)
}

// SaveSession 保存会话到内存
func (s *MemoryStore) SaveSession(_ context.Context, sessionID string, data []byte, _ time.Duration) error {
	s.mu.Lock()
	s.sessions[sessionID] = data
	s.mu.Unlock()
	return nil
}

// LoadSession 从内存加载会话
func (s *MemoryStore) LoadSession(_ context.Context, sessionID string) ([]byte, error) {
	s.mu.RLock()
	data, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	return data, nil
}
