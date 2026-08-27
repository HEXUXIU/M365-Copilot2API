package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const redisResponsesStatePrefix = "m365:responses-state:v1:"

type responseReplay struct {
	Status int
	Header http.Header
	Body   []byte
}

type responseClaim struct {
	Namespace string
	Response  string
	LeaseID   string
	Digest    string
	Messages  []oaiMsg
	ToolCount int
	Version   int64
}

type responseStateError struct {
	Status  int
	Type    string
	Message string
}

func (e *responseStateError) Error() string { return e.Message }

func responseRequestDigest(body responsesRequest) string {
	raw, _ := json.Marshal(body)
	return hashString(string(raw))
}

func responseStateKey(namespace, responseID string) string {
	return redisResponsesStatePrefix + hashString(namespace) + ":" + hashString(responseID)
}

func (s *Server) responseStateTTL() time.Duration {
	if s.affinity != nil && s.affinity.config.TTL > 0 {
		return s.affinity.config.TTL
	}
	return time.Hour
}

func (s *Server) responseLeaseTTL() time.Duration {
	ttl := 6 * time.Minute
	if s.affinity != nil && s.affinity.config.LockTTL > 0 {
		ttl = s.affinity.config.LockTTL
	}
	if s.settings != nil {
		if requestTTL := time.Duration(s.settings.get().ChatTimeoutSeconds)*time.Second + 30*time.Second; requestTTL > ttl {
			ttl = requestTTL
		}
	}
	return ttl
}

func (s *Server) responseRedis(ctx context.Context) *redisAffinityStore {
	if s.affinity == nil || s.affinity.config.Mode == affinityOff {
		return nil
	}
	store := s.affinity.store(ctx)
	redisStore, _ := store.(*redisAffinityStore)
	return redisStore
}

// loadResponseNodeLocked requires responseMu. Redis stores only hashes in its
// key, so neither bearer credentials nor public response ids are exposed.
func (s *Server) loadResponseNodeLocked(ctx context.Context, namespace, responseID string) (*RespNode, bool) {
	store := s.responseRedis(ctx)
	if store == nil {
		if bucket := s.responseMessages[namespace]; bucket != nil {
			if node := bucket[responseID]; node != nil {
				return node, true
			}
		}
		return nil, false
	}
	raw, err := store.client.Get(ctx, responseStateKey(namespace, responseID)).Bytes()
	if errors.Is(err, redis.Nil) {
		if bucket := s.responseMessages[namespace]; bucket != nil {
			if node := bucket[responseID]; node != nil && time.Since(node.At) <= s.responseStateTTL() {
				if encoded, encodeErr := json.Marshal(node); encodeErr == nil {
					_ = store.client.Set(ctx, responseStateKey(namespace, responseID), encoded, s.responseStateTTL()).Err()
				}
				return node, true
			}
		}
		return nil, false
	}
	if err != nil {
		s.affinity.markStoreError(err)
		return nil, false
	}
	var node RespNode
	if err := json.Unmarshal(raw, &node); err != nil {
		log.Printf("[responses-state] invalid persisted state key=%s: %v", shortPrefix(hashString(responseID)), err)
		return nil, false
	}
	if time.Since(node.At) > s.responseStateTTL() {
		return nil, false
	}
	bucket := s.responseMessages[namespace]
	if bucket == nil {
		bucket = map[string]*RespNode{}
		s.responseMessages[namespace] = bucket
	}
	bucket[responseID] = &node
	return &node, true
}

func (s *Server) persistResponseNodeLocked(ctx context.Context, namespace, responseID string, node *RespNode) {
	bucket := s.responseMessages[namespace]
	if bucket == nil {
		bucket = map[string]*RespNode{}
		s.responseMessages[namespace] = bucket
	}
	for key, history := range bucket {
		if time.Since(history.At) > s.responseStateTTL() {
			delete(bucket, key)
		}
	}
	if len(bucket) >= maxResponsesPerTenant && bucket[responseID] == nil {
		var oldestKey string
		var oldestAt time.Time
		for key, history := range bucket {
			if oldestKey == "" || history.At.Before(oldestAt) {
				oldestKey, oldestAt = key, history.At
			}
		}
		delete(bucket, oldestKey)
	}
	bucket[responseID] = node
	store := s.responseRedis(ctx)
	if store == nil {
		return
	}
	raw, err := json.Marshal(node)
	if err == nil {
		err = store.client.Set(ctx, responseStateKey(namespace, responseID), raw, s.responseStateTTL()).Err()
	}
	if err != nil {
		s.affinity.markStoreError(err)
		log.Printf("[responses-state] persist degraded key=%s: %v", shortPrefix(hashString(responseID)), err)
	}
}

func (s *Server) responseStateLock(ctx context.Context, namespace, responseID string) (func(), error) {
	if s.affinity == nil {
		return func() {}, nil
	}
	store := s.affinity.store(ctx)
	if store == nil {
		return func() {}, nil
	}
	wait := 2 * time.Second
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
		wait = time.Until(deadline)
	}
	lockKey := "responses-state:" + hashString(namespace) + ":" + hashString(responseID)
	release, err := store.Acquire(ctx, lockKey, 10*time.Second, wait)
	if err == nil {
		return release, nil
	}
	if errors.Is(err, errAffinityLockTimeout) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, err
	}
	fallback := s.affinity.markStoreError(err)
	if fallback != nil && fallback != store {
		return fallback.Acquire(ctx, lockKey, 10*time.Second, wait)
	}
	return nil, err
}

func (s *Server) claimResponse(ctx context.Context, namespace, responseID, digest, tenant, sessionID string, toolIDs []string) (*responseClaim, *responseReplay, error) {
	deadline := time.Now().Add(10 * time.Second)
	for {
		release, err := s.responseStateLock(ctx, namespace, responseID)
		if err != nil {
			return nil, nil, &responseStateError{Status: http.StatusConflict, Type: "conflict", Message: "previous_response_id is busy"}
		}
		s.responseMu.Lock()
		node, ok := s.loadResponseNodeLocked(ctx, namespace, responseID)
		if !ok || len(node.Messages) == 0 {
			s.responseMu.Unlock()
			release()
			return nil, nil, &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "unknown previous_response_id"}
		}
		if node.Tenant != "" && node.Tenant != tenant {
			s.responseMu.Unlock()
			release()
			return nil, nil, &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "previous_response_id tenant mismatch"}
		}
		if node.SessionID != sessionID {
			s.responseMu.Unlock()
			release()
			return nil, nil, &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "previous_response_id session mismatch"}
		}
		if node.Consumed {
			if node.ConsumedDigest == digest && len(node.ReplayBody) > 0 {
				replay := &responseReplay{Status: node.ReplayStatus, Header: node.ReplayHeader.Clone(), Body: append([]byte(nil), node.ReplayBody...)}
				s.responseMu.Unlock()
				release()
				return nil, replay, nil
			}
			version := node.Version
			s.responseMu.Unlock()
			release()
			return nil, nil, &responseStateError{Status: http.StatusConflict, Type: "conflict", Message: fmt.Sprintf("previous_response_id already consumed at version %d", version)}
		}
		if err := validateResponseToolOutputs(node, toolIDs); err != nil {
			s.responseMu.Unlock()
			release()
			return nil, nil, err
		}
		now := time.Now()
		if node.LeaseID != "" && node.LeaseUntil.After(now) {
			if node.LeaseDigest != digest {
				s.responseMu.Unlock()
				release()
				return nil, nil, &responseStateError{Status: http.StatusConflict, Type: "conflict", Message: "previous_response_id is processing another request"}
			}
			wait := node.wait
			if wait == nil {
				wait = make(chan struct{})
				node.wait = wait
			}
			s.responseMu.Unlock()
			release()
			if time.Now().After(deadline) {
				return nil, nil, &responseStateError{Status: http.StatusConflict, Type: "conflict", Message: "previous_response_id is still processing"}
			}
			select {
			case <-ctx.Done():
				return nil, nil, ctx.Err()
			case <-wait:
			case <-time.After(100 * time.Millisecond):
			}
			continue
		}
		node.LeaseID = uuid.NewString()
		node.LeaseDigest = digest
		node.LeaseUntil = now.Add(s.responseLeaseTTL())
		node.Version++
		node.wait = make(chan struct{})
		s.persistResponseNodeLocked(ctx, namespace, responseID, node)
		claim := &responseClaim{
			Namespace: namespace, Response: responseID, LeaseID: node.LeaseID, Digest: digest,
			Messages: append([]oaiMsg(nil), node.Messages...), ToolCount: len(node.ToolCalls), Version: node.Version,
		}
		s.responseMu.Unlock()
		release()
		return claim, nil, nil
	}
}

func validateResponseToolOutputs(node *RespNode, toolIDs []string) error {
	if len(toolIDs) == 0 {
		if len(node.ToolCalls) > 0 {
			return &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "previous_response_id expects tool outputs for pending calls"}
		}
		return nil
	}
	if len(node.ToolCalls) == 0 {
		return &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "previous_response_id has no pending tool calls"}
	}
	seen := make(map[string]bool, len(toolIDs))
	for _, id := range toolIDs {
		if seen[id] {
			return &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "duplicate call_id: " + id}
		}
		seen[id] = true
		if _, ok := node.ToolCalls[id]; !ok {
			return &responseStateError{Status: http.StatusBadRequest, Type: "invalid_request_error", Message: "call_id not in parent pending set: " + id}
		}
	}
	return nil
}

func (s *Server) finishResponseClaim(ctx context.Context, claim *responseClaim, childID string, replay responseReplay) error {
	if claim == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	release, err := s.responseStateLock(ctx, claim.Namespace, claim.Response)
	if err != nil {
		return err
	}
	defer release()
	s.responseMu.Lock()
	defer s.responseMu.Unlock()
	node, ok := s.loadResponseNodeLocked(ctx, claim.Namespace, claim.Response)
	if !ok || node.LeaseID != claim.LeaseID || node.LeaseDigest != claim.Digest {
		return errors.New("response lease lost before commit")
	}
	node.Consumed = true
	node.ConsumedDigest = claim.Digest
	node.ChildID = childID
	node.LeaseID = ""
	node.LeaseDigest = ""
	node.LeaseUntil = time.Time{}
	node.ReplayStatus = replay.Status
	node.ReplayHeader = replay.Header.Clone()
	node.ReplayBody = append([]byte(nil), replay.Body...)
	closeResponseWaiter(node)
	s.persistResponseNodeLocked(ctx, claim.Namespace, claim.Response, node)
	return nil
}

func (s *Server) releaseResponseClaim(ctx context.Context, claim *responseClaim) {
	if claim == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	release, err := s.responseStateLock(ctx, claim.Namespace, claim.Response)
	if err != nil {
		return
	}
	defer release()
	s.responseMu.Lock()
	defer s.responseMu.Unlock()
	node, ok := s.loadResponseNodeLocked(ctx, claim.Namespace, claim.Response)
	if !ok || node.LeaseID != claim.LeaseID {
		return
	}
	node.LeaseID = ""
	node.LeaseDigest = ""
	node.LeaseUntil = time.Time{}
	closeResponseWaiter(node)
	s.persistResponseNodeLocked(ctx, claim.Namespace, claim.Response, node)
}

func closeResponseWaiter(node *RespNode) {
	if node.wait != nil {
		close(node.wait)
		node.wait = nil
	}
}

func writeResponseReplay(w http.ResponseWriter, replay *responseReplay) {
	for key, values := range replay.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := replay.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(replay.Body)
}

type captureResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (w *captureResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *captureResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *captureResponseWriter) Write(value []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	_, _ = w.body.Write(value)
	return w.ResponseWriter.Write(value)
}

func (w *captureResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *captureResponseWriter) replay() responseReplay {
	header := make(http.Header)
	for key, values := range w.Header() {
		header[key] = append([]string(nil), values...)
	}
	return responseReplay{Status: w.status, Header: header, Body: append([]byte(nil), w.body.Bytes()...)}
}

func responseStateAudit(claim *responseClaim, action string) {
	if claim == nil {
		return
	}
	log.Printf("[responses-state] previous=%s action=%s version=%d digest=%s", shortPrefix(hashString(claim.Response)), action, claim.Version, shortPrefix(claim.Digest))
}

func responseStateErrorFields(err error) (int, string, string) {
	var stateErr *responseStateError
	if errors.As(err, &stateErr) {
		return stateErr.Status, stateErr.Type, stateErr.Message
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return http.StatusRequestTimeout, "request_timeout", "response state wait canceled"
	}
	return http.StatusConflict, "conflict", strings.TrimSpace(err.Error())
}
