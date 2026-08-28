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

func redisHistoryIDs(raw any) []string {
	text, ok := raw.(string)
	if !ok || text == "" {
		return nil
	}
	var ids []string
	if json.Unmarshal([]byte(text), &ids) == nil && len(ids) > 0 {
		return ids
	}
	return []string{text}
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
	bindingIDs := make([]string, 0, len(values))
	positions := make([]int, 0, len(values))
	for index, value := range values {
		ids := redisHistoryIDs(value)
		for _, id := range ids {
			if id == "" {
				continue
			}
			bindingIDs = append(bindingIDs, id)
			positions = append(positions, index)
		}
	}
	if len(bindingIDs) == 0 {
		return affinityBinding{}, 0, false, nil
	}
	bindingKeys := make([]string, len(bindingIDs))
	for i, id := range bindingIDs {
		bindingKeys[i] = redisBindingKey(id)
	}
	rawBindings, err := s.client.MGet(ctx, bindingKeys...).Result()
	if err != nil {
		return affinityBinding{}, 0, false, err
	}
	for i, raw := range rawBindings {
		text, ok := raw.(string)
		if !ok || text == "" {
			continue
		}
		var binding affinityBinding
		if err := json.Unmarshal([]byte(text), &binding); err != nil {
			return affinityBinding{}, 0, false, err
		}
		if binding.ID != "" && binding.TenantHash == tenantHash {
			return binding, positions[i], true, nil
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

// PutBindingScript updates the binding, history index, and LRU score as one
// Redis operation. This prevents concurrent conversations sharing a prefix
// from overwriting each other's history index between separate commands.
var redisPutBindingScript = redis.NewScript(`
local binding_key = KEYS[1]
local lru_key = KEYS[2]
local raw = ARGV[1]
local ttl = tonumber(ARGV[2])
local history_prefix = ARGV[3]
local score = ARGV[4]
local old_raw = redis.call('GET', binding_key)
if old_raw then
  local old_ok, old = pcall(cjson.decode, old_raw)
  if old_ok and old.history_digest and old.history_digest ~= '' then
    local old_key = history_prefix .. old.tenant_hash .. ':' .. old.history_digest
    local history_raw = redis.call('GET', old_key)
    if history_raw then
      local ids_ok, ids = pcall(cjson.decode, history_raw)
      if ids_ok and type(ids) == 'table' then
        local kept = {}
        for _, id in ipairs(ids) do
          if id ~= old.id then table.insert(kept, id) end
        end
        if #kept > 0 then
          local pttl = redis.call('PTTL', old_key)
          if pttl <= 0 then pttl = ttl end
          redis.call('SET', old_key, cjson.encode(kept), 'PX', pttl)
        else
          redis.call('DEL', old_key)
        end
      elseif history_raw == old.id then
        redis.call('DEL', old_key)
      end
    end
  end
end
redis.call('SET', binding_key, raw, 'PX', ttl)
local binding = cjson.decode(raw)
if binding.history_digest and binding.history_digest ~= '' then
  local new_key = history_prefix .. binding.tenant_hash .. ':' .. binding.history_digest
  local history_raw = redis.call('GET', new_key)
  local ids = {}
  if history_raw then
    local ids_ok, decoded = pcall(cjson.decode, history_raw)
    if ids_ok and type(decoded) == 'table' then
      ids = decoded
    elseif history_raw ~= '' then
      ids = {history_raw}
    end
  end
  local found = false
  for _, id in ipairs(ids) do if id == binding.id then found = true end end
  if not found then table.insert(ids, binding.id) end
  redis.call('SET', new_key, cjson.encode(ids), 'PX', ttl)
end
redis.call('ZADD', lru_key, score, binding.id)
return 1
`)

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
	_, err = redisPutBindingScript.Run(ctx, s.client, []string{redisBindingKey(binding.ID), redisLRUKey}, raw, ttl.Milliseconds(), redisHistoryPrefix, binding.LastUsedAt.UnixMilli()).Result()
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
  local history_key = ARGV[4] .. current.tenant_hash .. ':' .. current.history_digest
  local history_raw = redis.call('GET', history_key)
  if history_raw then
    local ids_ok, ids = pcall(cjson.decode, history_raw)
    if not ids_ok or type(ids) ~= 'table' then
      ids = {history_raw}
    end
    local kept = {}
    for _, id in ipairs(ids) do
      if id ~= current.id then table.insert(kept, id) end
    end
    if #kept > 0 then
      redis.call('SET', history_key, cjson.encode(kept), 'PX', ARGV[3])
    else
      redis.call('DEL', history_key)
    end
  end
end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
local replacement = cjson.decode(ARGV[2])
if replacement.history_digest and replacement.history_digest ~= '' then
  local replacement_key = ARGV[4] .. replacement.tenant_hash .. ':' .. replacement.history_digest
  local replacement_raw = redis.call('GET', replacement_key)
  local ids = {}
  if replacement_raw then
    local ids_ok, decoded = pcall(cjson.decode, replacement_raw)
    if ids_ok and type(decoded) == 'table' then ids = decoded
    elseif replacement_raw ~= '' then ids = {replacement_raw} end
  end
  local found = false
  for _, id in ipairs(ids) do if id == replacement.id then found = true end end
  if not found then table.insert(ids, replacement.id) end
  redis.call('SET', replacement_key, cjson.encode(ids), 'PX', ARGV[3])
end
redis.call('ZADD', KEYS[2], ARGV[5], replacement.id)
return 1
`)

// Remove one binding from a shared history index without deleting siblings.
var redisRemoveHistoryIDScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local ok, ids = pcall(cjson.decode, raw)
if not ok or type(ids) ~= 'table' then
  if raw == ARGV[1] then redis.call('DEL', KEYS[1]); return 1 end
  return 0
end
local kept, removed = {}, 0
for _, id in ipairs(ids) do
  if id == ARGV[1] then removed = 1 else table.insert(kept, id) end
end
if removed == 0 then return 0 end
if #kept == 0 then
  redis.call('DEL', KEYS[1])
else
  local pttl = redis.call('PTTL', KEYS[1])
  if pttl <= 0 then pttl = tonumber(ARGV[2]) end
  redis.call('SET', KEYS[1], cjson.encode(kept), 'PX', pttl)
end
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
		binding, found, getErr := s.GetBinding(ctx, id)
		if getErr != nil {
			return getErr
		}
		if found && binding.HistoryDigest != "" {
			if _, err := redisRemoveHistoryIDScript.Run(ctx, s.client, []string{redisHistoryKey(binding.TenantHash, binding.HistoryDigest)}, id, s.ttl.Milliseconds()).Result(); err != nil {
				return err
			}
		}
		pipe := s.client.Pipeline()
		pipe.Del(ctx, redisBindingKey(id))
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
	owner, err := randomOwner()
	if err != nil {
		return nil, err
	}
	lockKey := redisLockKey(key)
	deadline := time.Now().Add(wait)
	backoff := 20 * time.Millisecond
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
			return nil, errAffinityLockTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 500*time.Millisecond {
			backoff *= 2
			if backoff > 500*time.Millisecond {
				backoff = 500 * time.Millisecond
			}
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
