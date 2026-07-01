// Package cache is a thin, optional Redis wrapper. When no Redis URL is
// configured the cache degrades to a no-op so the app still runs.
package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type Cache struct {
	rdb *redis.Client
}

// New returns a Cache. If url is empty, the returned Cache is a no-op.
func New(url string) (*Cache, error) {
	if url == "" {
		return &Cache{}, nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	return &Cache{rdb: redis.NewClient(opt)}, nil
}

func (c *Cache) Enabled() bool { return c != nil && c.rdb != nil }

func (c *Cache) Get(ctx context.Context, key string) ([]byte, bool) {
	if !c.Enabled() {
		return nil, false
	}
	b, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	return b, true
}

func (c *Cache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) {
	if !c.Enabled() {
		return
	}
	_ = c.rdb.Set(ctx, key, val, ttl).Err()
}

// DelPrefix removes all keys with the given prefix. Used to invalidate a
// tenant's cached lists after a write.
func (c *Cache) DelPrefix(ctx context.Context, prefix string) {
	if !c.Enabled() {
		return
	}
	iter := c.rdb.Scan(ctx, 0, prefix+"*", 100).Iterator()
	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		_ = c.rdb.Del(ctx, keys...).Err()
	}
}

func (c *Cache) Ping(ctx context.Context) error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Ping(ctx).Err()
}

func (c *Cache) Close() error {
	if !c.Enabled() {
		return nil
	}
	return c.rdb.Close()
}
