package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisAccountPrefix  = "m365:account-affinity:v1:"
	redisBindingPrefix  = "m365:conversation:v1:"
	redisHistoryPrefix  = "m365:history:v1:"
	redisLockPrefix     = "m365:lock:v1:"
	redisHealthPrefix   = "m365:account-health:v1:"
	redisResponsePrefix = "m365:response:v1:"
	redisLRUKey         = "m365:affinity-lru:v1"
)

type redisAffinityStore struct {
	client *redis.Client
	ttl    time.Duration
	max    int64
}

func newRedisAffinityStore(rawURL string, poolSize int, ttl time.Duration, max int) (*redisAffinityStore, error) {
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}
	if poolSize > 0 {
		options.PoolSize = poolSize
	}
	options.MaxRetries = 1
	options.DialTimeout = 2 * time.Second
	options.ReadTimeout = 2 * time.Second
	options.WriteTimeout = 2 * time.Second
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	if max <= 0 {
		max = 10000
	}
	return &redisAffinityStore{client: redis.NewClient(options), ttl: ttl, max: int64(max)}, nil
}

func redisAccountKey(tenantHash, affinityHash string) string {
	return redisAccountPrefix + tenantHash + ":" + affinityHash
}

func redisBindingKey(bindingID string) string { return redisBindingPrefix + bindingID }
func redisHistoryKey(tenantHash, digest string) string {
	return redisHistoryPrefix + tenantHash + ":" + digest
}
func redisLockKey(key string) string         { return redisLockPrefix + hashString(key) }
func redisHealthKey(accountID string) string { return redisHealthPrefix + hashString(accountID) }
func redisResponseKey(tenantHash, responseHash string) string {
	return redisResponsePrefix + tenantHash + ":" + responseHash
}

func (s *redisAffinityStore) GetAccount(ctx context.Context, tenantHash, affinityHash string) (string, bool, error) {
	value, err := s.client.Get(ctx, redisAccountKey(tenantHash, affinityHash)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (s *redisAffinityStore) SetAccount(ctx context.Context, tenantHash, affinityHash, accountID string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	return s.client.Set(ctx, redisAccountKey(tenantHash, affinityHash), accountID, ttl).Err()
}

func (s *redisAffinityStore) GetResponse(ctx context.Context, tenantHash, responseHash string) (string, bool, error) {
	value, err := s.client.Get(ctx, redisResponseKey(tenantHash, responseHash)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	return value, err == nil, err
}

func (s *redisAffinityStore) SetResponse(ctx context.Context, tenantHash, responseHash, bindingID string, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	return s.client.Set(ctx, redisResponseKey(tenantHash, responseHash), bindingID, ttl).Err()
}

func (s *redisAffinityStore) GetBinding(ctx context.Context, id string) (affinityBinding, bool, error) {
	raw, err := s.client.Get(ctx, redisBindingKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return affinityBinding{}, false, nil
	}
	if err != nil {
		return affinityBinding{}, false, err
	}
	var binding affinityBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return affinityBinding{}, false, err
	}
	return binding, true, nil
}

func (s *redisAffinityStore) FindHistory(ctx context.Context, tenantHash string, digests []string) (affinityBinding, int, bool, error) {
	if len(digests) == 0 {
		return affinityBinding{}, 0, false, nil
	}
	keys := make([]string, len(digests))
	for i, digest := range digests {
		keys[i] = redisHistoryKey(tenantHash, digest)
	}
	values, err := s.client.MGet(ctx, keys...).Result()
	if err != nil {
		return affinityBinding{}, 0, false, err
	}
	for index, value := range values {
		id, ok := value.(string)
		if !ok || id == "" {
			continue
		}
		binding, found, err := s.GetBinding(ctx, id)
		if err != nil {
			return affinityBinding{}, 0, false, err
		}
		if found {
			return binding, index, true, nil
		}
	}
	return affinityBinding{}, 0, false, nil
}

func prepareBinding(binding affinityBinding) affinityBinding {
	now := time.Now().UTC()
	if binding.CreatedAt.IsZero() {
		binding.CreatedAt = now
	}
	binding.LastUsedAt = now
	if binding.Generation == 0 {
		binding.Generation = 1
	}
	return binding
}

func (s *redisAffinityStore) PutBinding(ctx context.Context, binding affinityBinding, ttl time.Duration) error {
	if binding.ID == "" || binding.TenantHash == "" {
		return errors.New("affinity binding id and tenant are required")
	}
	if ttl <= 0 {
		ttl = s.ttl
	}
	binding = prepareBinding(binding)
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	old, found, err := s.GetBinding(ctx, binding.ID)
	if err != nil {
		return err
	}
	_, err = s.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		if found && old.HistoryDigest != "" && old.HistoryDigest != binding.HistoryDigest {
			pipe.Del(ctx, redisHistoryKey(old.TenantHash, old.HistoryDigest))
		}
		pipe.Set(ctx, redisBindingKey(binding.ID), raw, ttl)
		if binding.HistoryDigest != "" {
			pipe.Set(ctx, redisHistoryKey(binding.TenantHash, binding.HistoryDigest), binding.ID, ttl)
		}
		pipe.ZAdd(ctx, redisLRUKey, redis.Z{Score: float64(binding.LastUsedAt.UnixMilli()), Member: binding.ID})
		return nil
	})
	if err != nil {
		return err
	}
	return s.evict(ctx)
}

var redisBindingCASScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local current = cjson.decode(raw)
if tonumber(current.generation) ~= tonumber(ARGV[1]) then return 0 end
if current.history_digest and current.history_digest ~= '' then
  redis.call('DEL', ARGV[4] .. current.tenant_hash .. ':' .. current.history_digest)
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
local replacement = cjson.decode(ARGV[2])
if replacement.history_digest and replacement.history_digest ~= '' then
  redis.call('SET', ARGV[4] .. replacement.tenant_hash .. ':' .. replacement.history_digest, replacement.id, 'PX', ARGV[3])
end
redis.call('ZADD', KEYS[2], ARGV[5], replacement.id)
return 1
`)

func (s *redisAffinityStore) CompareAndSwapBinding(ctx context.Context, id string, generation int64, binding affinityBinding, ttl time.Duration) (bool, error) {
	if ttl <= 0 {
		ttl = s.ttl
	}
	binding = prepareBinding(binding)
	raw, err := json.Marshal(binding)
	if err != nil {
		return false, err
	}
	value, err := redisBindingCASScript.Run(ctx, s.client, []string{redisBindingKey(id), redisLRUKey}, generation, raw, ttl.Milliseconds(), redisHistoryPrefix, binding.LastUsedAt.UnixMilli()).Int64()
	if err != nil {
		return false, err
	}
	if value == 1 {
		if err := s.evict(ctx); err != nil {
			return false, err
		}
	}
	return value == 1, nil
}

func (s *redisAffinityStore) evict(ctx context.Context) error {
	count, err := s.client.ZCard(ctx, redisLRUKey).Result()
	if err != nil || count <= s.max {
		return err
	}
	items, err := s.client.ZPopMin(ctx, redisLRUKey, count-s.max).Result()
	if err != nil {
		return err
	}
	for _, item := range items {
		id := fmt.Sprint(item.Member)
		binding, ok, getErr := s.GetBinding(ctx, id)
		if getErr != nil {
			return getErr
		}
		pipe := s.client.Pipeline()
		pipe.Del(ctx, redisBindingKey(id))
		if ok && binding.HistoryDigest != "" {
			pipe.Del(ctx, redisHistoryKey(binding.TenantHash, binding.HistoryDigest))
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

var redisUnlockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func (s *redisAffinityStore) Acquire(ctx context.Context, key string, ttl, wait time.Duration) (func(), error) {
	if ttl <= 0 {
		ttl = 180 * time.Second
	}
	if wait <= 0 {
		wait = 120 * time.Second
	}
	owner := randomOwner()
	lockKey := redisLockKey(key)
	deadline := time.Now().Add(wait)
	for {
		ok, err := s.client.SetNX(ctx, lockKey, owner, ttl).Result()
		if err != nil {
			return nil, err
		}
		if ok {
			return func() {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = redisUnlockScript.Run(releaseCtx, s.client, []string{lockKey}, owner).Result()
			}, nil
		}
		if time.Now().After(deadline) {
			return nil, errors.New("session affinity lock timeout")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (s *redisAffinityStore) Healthy(ctx context.Context) bool {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return s.client.Ping(checkCtx).Err() == nil
}

func (s *redisAffinityStore) GetAccountHealth(ctx context.Context, accountID string) (affinityAccountHealth, bool, error) {
	raw, err := s.client.Get(ctx, redisHealthKey(accountID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return affinityAccountHealth{}, false, nil
	}
	if err != nil {
		return affinityAccountHealth{}, false, err
	}
	var health affinityAccountHealth
	if err := json.Unmarshal(raw, &health); err != nil {
		return affinityAccountHealth{}, false, err
	}
	return health, true, nil
}

func (s *redisAffinityStore) SetAccountHealth(ctx context.Context, accountID string, health affinityAccountHealth, ttl time.Duration) error {
	if ttl <= 0 {
		ttl = s.ttl
	}
	raw, err := json.Marshal(health)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, redisHealthKey(accountID), raw, ttl).Err()
}

func (s *redisAffinityStore) ClearAccountHealth(ctx context.Context, accountID string) error {
	return s.client.Del(ctx, redisHealthKey(accountID)).Err()
}

func (s *redisAffinityStore) Close() error { return s.client.Close() }
