package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"scraper/internal/logger"
	"time"

	redisv8 "github.com/go-redis/redis/v8"
	"github.com/hibiken/asynq"
)

type Options struct {
	Addr     string
	Password string
}

type Service struct {
	client *redisv8.Client
	log    *logger.Logger
}

func New(opts Options) (*Service, error) {
	c := redisv8.NewClient(&redisv8.Options{Addr: opts.Addr, Password: opts.Password})
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return &Service{client: c, log: logger.New("Redis")}, nil
}

func (s *Service) Close() error            { return s.client.Close() }
func (s *Service) Client() *redisv8.Client { return s.client }

func (s *Service) HealthCheck(ctx context.Context) error {
	// 1. Basic ping check
	if err := s.client.Ping(ctx).Err(); err != nil {
		s.log.LogErrorf("Redis health check failed: %v", err)
		return fmt.Errorf("redis ping failed: %v", err)
	}

	// 2. Simple write/read test to verify Redis is working
	testKey := "health:test:" + time.Now().Format("20060102150405")
	testValue := "ok"

	// Write test
	err := s.client.Set(ctx, testKey, testValue, 10*time.Second).Err()
	if err != nil {
		return fmt.Errorf("redis write test failed: %v", err)
	}

	// Read test
	val, err := s.client.Get(ctx, testKey).Result()
	if err != nil {
		return fmt.Errorf("redis read test failed: %v", err)
	}

	if val != testValue {
		return fmt.Errorf("redis value mismatch: got %s, want %s", val, testValue)
	}

	// Clean up test key
	_ = s.client.Del(ctx, testKey).Err()

	return nil
}

func (s *Service) AsynqRedisOpt() asynq.RedisClientOpt {
	return asynq.RedisClientOpt{Addr: s.client.Options().Addr, Password: s.client.Options().Password}
}

// Cache helpers
func (s *Service) CacheGet(ctx context.Context, key string, dest interface{}) error {
	b, err := s.client.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dest)
}

func (s *Service) CacheSet(ctx context.Context, key string, val interface{}, ttlSeconds int) error {
	b, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, key, b, time.Duration(ttlSeconds)*time.Second).Err()
}
