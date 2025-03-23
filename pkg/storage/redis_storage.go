// MIT License
//
// Copyright (c) 2024 PlexObject Solutions, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// This file implements the Redis-based storage backend for leader election locks.
// It provides reliable distributed locking using Redis's atomic operations and
// Lua scripts to ensure lock consistency.
//
// The implementation provides:
// - Structured lock metadata with versioning
// - Atomic lock operations using Redis Lua scripts
// - TTL-based lock expiration to prevent stale locks
// - Backward compatibility with older lock formats
// - Robust error handling and verification
//
// Redis is the recommended storage backend for most deployments due to its
// performance, reliability, and built-in support for TTL-based keys.

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/go-redis/redis/v8"
	"k8s.io/klog/v2"
)

// RedisStorage implements the LeaderStorage interface using Redis.
// It uses a combination of Redis commands and Lua scripts to provide
// atomic operations for distributed locking.
type RedisStorage struct {
	client *redis.Client
}

// NewRedisStorage creates a new Redis storage
func NewRedisStorage(client *redis.Client) *RedisStorage {
	return &RedisStorage{
		client: client,
	}
}

// TryAcquireLock attempts to acquire a lock using Redis SETNX operation.
// It creates lock metadata with versioning and sets a TTL to prevent
// stale locks. The operation is atomic and only succeeds if the key
// doesn't already exist.
func (s *RedisStorage) TryAcquireLock(ctx context.Context, key string, value string,
	clusterName string, ttl time.Duration) (bool, error) {
	// Create metadata
	now := time.Now()
	version := uint64(now.UnixNano()) // Use timestamp as initial version
	metadata := LockMetadata{
		Value:       value,
		ClusterName: clusterName,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(ttl),
		TTL:         ttl,
		Version:     version,
	}

	// Serialize metadata to JSON
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}

	// Try to set the key with NX (only if it doesn't exist) and expiration
	success, err := s.client.SetNX(ctx, key, string(metadataBytes), ttl).Result()
	if err != nil {
		return false, err
	}

	if success {
		klog.V(4).Infof("Acquired lock %s for %s on cluster %s, expires in %v, version %d",
			key, value, clusterName, ttl, version)
	}

	return success, nil
}

// RenewLock extends the TTL of an existing lock, but only if the caller
// still owns the lock. It uses a Lua script to atomically check ownership
// and update the lock in a single operation to prevent race conditions.
func (s *RedisStorage) RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	// First check if we still own the lock
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return false, err
	}

	if metadata == nil {
		return false, nil // Key doesn't exist
	}

	if metadata.Value != value {
		return false, nil // Someone else owns the lock
	}

	// Update metadata
	now := time.Now()
	newVersion := metadata.Version + 1
	metadata.UpdatedAt = now
	metadata.ExpiresAt = now.Add(ttl)
	metadata.TTL = ttl
	metadata.Version = newVersion

	// Serialize updated metadata
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return false, err
	}

	// Use a Lua script to atomically check and update the lock
	script := `
	local metadata = redis.call("get", KEYS[1])
	if metadata ~= false then
		local decoded = cjson.decode(metadata)
		if decoded.value == ARGV[1] then
			redis.call("setex", KEYS[1], ARGV[2], ARGV[3])
			return 1
		end
	end
	return 0
	`

	result, err := s.client.Eval(ctx, script, []string{key}, value, int(ttl.Seconds()), string(metadataBytes)).Result()
	if err != nil {
		return false, err
	}

	success := result.(int64) == 1
	if success {
		klog.V(4).Infof("Renewed lock %s for %s, expires in %v, version %d",
			key, value, ttl, newVersion)
	}

	return success, nil
}

// ReleaseLock releases a lock, but only if the caller owns it. It uses a
// Lua script to atomically verify ownership and delete the key in a single
// operation. It also performs additional verification to ensure the lock
// is actually released.
func (s *RedisStorage) ReleaseLock(ctx context.Context, key string, value string) (bool, error) {
	// Use Lua script to ensure we only delete our own lock
	script := `
    local metadata = redis.call("get", KEYS[1])
    if metadata ~= false then
        local decoded = cjson.decode(metadata)
        if decoded.value == ARGV[1] then
            redis.call("del", KEYS[1])
            return 1
        end
    end
    return 0
    `

	result, err := s.client.Eval(ctx, script, []string{key}, value).Result()
	if err != nil {
		return false, err
	}

	success := result.(int64) == 1
	if success {
		klog.V(4).Infof("Released lock %s for %s", key, value)

		// CRITICAL FIX: Verify the key is actually gone
		val, err := s.client.Get(ctx, key).Result()
		if err == nil && val != "" {
			klog.Warningf("Lock still exists after release! Key: %s, Value: %s", key, val)
			// Force delete the key
			s.client.Del(ctx, key)
		}
	}

	return success, nil
}

// GetLockInfo gets the current metadata of a lock with the given key
func (s *RedisStorage) GetLockInfo(ctx context.Context, key string) (*LockMetadata, error) {
	metadataStr, err := s.client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil // Key doesn't exist
		}
		return nil, err
	}

	var metadata LockMetadata
	if err := json.Unmarshal([]byte(metadataStr), &metadata); err != nil {
		// If we can't unmarshal, it might be an old-format lock
		// Try to handle it gracefully
		klog.Warningf("Failed to unmarshal lock metadata for key %s: %v. Treating as legacy format.", key, err)

		// Create a basic metadata with just the value
		now := time.Now()
		ttl, _ := s.client.TTL(ctx, key).Result()

		metadata = LockMetadata{
			Value:       metadataStr,   // Use the raw string as the value
			ClusterName: "unknown",     // We don't know the cluster name
			CreatedAt:   now.Add(-ttl), // Estimate creation time
			UpdatedAt:   now,
			ExpiresAt:   now.Add(ttl),
			TTL:         ttl,
			Version:     uint64(now.UnixNano()), // Use current timestamp as version
		}
	} else {
		// Update the TTL based on the current time
		ttl, _ := s.client.TTL(ctx, key).Result()
		metadata.TTL = ttl
		metadata.ExpiresAt = time.Now().Add(ttl)
	}

	return &metadata, nil
}

// GetLockValue gets the current value of a lock with the given key (for backward compatibility)
func (s *RedisStorage) GetLockValue(ctx context.Context, key string) (string, error) {
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return "", err
	}

	if metadata == nil {
		return "", nil
	}

	return metadata.Value, nil
}

// GetLockTTL gets the remaining TTL of a lock with the given key (for backward compatibility)
func (s *RedisStorage) GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	ttl, err := s.client.TTL(ctx, key).Result()
	if err != nil {
		return 0, err
	}

	return ttl, nil
}

// Close closes the Redis connection
func (s *RedisStorage) Close() error {
	return s.client.Close()
}
