/**
 * @author: 公众号：IT杨秀才
 * @doc:后端，AI Agent知识进阶，后端、AI大模型、场景题面试大全：https://golangstar.cn/
 */

package memory

import (
	"context"
	"log"
	"time"

	imodel "interview-agent/internal/model"
)

// CombinedStore 组合存储：Redis 缓存 + MySQL 持久化
// 读取优先 Redis，miss 时回落 MySQL 并回填 Redis
// 写入双写 Redis + MySQL
type CombinedStore struct {
	redis *RedisStore
	mysql *MySQLStore
}

// NewCombinedStore 创建组合存储
func NewCombinedStore(redis *RedisStore, mysql *MySQLStore) *CombinedStore {
	return &CombinedStore{redis: redis, mysql: mysql}
}

// SaveProfile 双写：Redis + MySQL
func (s *CombinedStore) SaveProfile(ctx context.Context, profile *UserProfile) error {
	// 写 MySQL（持久化）
	if err := s.mysql.SaveProfile(ctx, profile); err != nil {
		return err
	}
	// 写 Redis（缓存，失败不影响主流程）
	if err := s.redis.SaveProfile(ctx, profile); err != nil {
		log.Printf("[CombinedStore] Redis 写入 profile 失败（不影响主流程）: %v", err)
	}
	return nil
}

// LoadProfile 先读 Redis，miss 则读 MySQL 并回填 Redis
func (s *CombinedStore) LoadProfile(ctx context.Context, userID string) (*UserProfile, error) {
	// 先读 Redis
	profile, err := s.redis.LoadProfile(ctx, userID)
	if err == nil && profile != nil {
		return profile, nil
	}

	// Redis miss，读 MySQL
	profile, err = s.mysql.LoadProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return nil, nil
	}

	// 回填 Redis
	if err := s.redis.SaveProfile(ctx, profile); err != nil {
		log.Printf("[CombinedStore] Redis 回填 profile 失败: %v", err)
	}

	return profile, nil
}

// SaveAbilityProfile 双写完整知识掌握画像到 MySQL 与 Redis；函数名为历史兼容名称。
func (s *CombinedStore) SaveAbilityProfile(ctx context.Context, profile *imodel.StudentAbilityProfile) error {
	if err := s.mysql.SaveAbilityProfile(ctx, profile); err != nil {
		return err
	}
	if err := s.redis.SaveAbilityProfile(ctx, profile); err != nil {
		log.Printf("[CombinedStore] Redis 写入知识掌握画像失败（不影响主流程）: %v", err)
	}
	return nil
}

// LoadAbilityProfile 优先读取 Redis，未命中时回落 MySQL 并回填缓存。
func (s *CombinedStore) LoadAbilityProfile(ctx context.Context, studentID string) (*imodel.StudentAbilityProfile, error) {
	profile, err := s.redis.LoadAbilityProfile(ctx, studentID)
	if err == nil && profile != nil {
		return profile, nil
	}
	profile, err = s.mysql.LoadAbilityProfile(ctx, studentID)
	if err != nil || profile == nil {
		return profile, err
	}
	if err := s.redis.SaveAbilityProfile(ctx, profile); err != nil {
		log.Printf("[CombinedStore] Redis 回填知识掌握画像失败: %v", err)
	}
	return profile, nil
}

// SaveSession 双写
func (s *CombinedStore) SaveSession(ctx context.Context, sessionID string, data []byte, ttl time.Duration) error {
	if err := s.mysql.SaveSession(ctx, sessionID, data, ttl); err != nil {
		return err
	}
	if err := s.redis.SaveSession(ctx, sessionID, data, ttl); err != nil {
		log.Printf("[CombinedStore] Redis 写入 session 失败: %v", err)
	}
	return nil
}

// LoadSession 先 Redis 后 MySQL
func (s *CombinedStore) LoadSession(ctx context.Context, sessionID string) ([]byte, error) {
	data, err := s.redis.LoadSession(ctx, sessionID)
	if err == nil && data != nil {
		return data, nil
	}

	data, err = s.mysql.LoadSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if data != nil {
		_ = s.redis.SaveSession(ctx, sessionID, data, 2*time.Hour)
	}
	return data, nil
}

// GetMySQLStore 获取底层 MySQL 存储（用于保存训练记录等扩展操作）。
func (s *CombinedStore) GetMySQLStore() *MySQLStore {
	return s.mysql
}
