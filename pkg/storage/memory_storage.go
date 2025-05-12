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

package storage

import (
	"context"
	"sync"
	"time"
)

// InMemoryLockEntry is the internal representation of a lock in InMemoryStorage.
type InMemoryLockEntry struct {
	LockMetadata
}

// InMemoryStorage implements the LeaderStorage interface using an in-memory map.
// This is intended for testing purposes and does not provide persistence.
type InMemoryStorage struct {
	mu    sync.RWMutex
	locks map[string]InMemoryLockEntry
}

// NewInMemoryStorage creates a new in-memory storage implementation.
func NewInMemoryStorage() *InMemoryStorage {
	return &InMemoryStorage{
		locks: make(map[string]InMemoryLockEntry),
	}
}

// TryAcquireLock attempts to acquire a lock with the given key, value, and TTL.
func (s *InMemoryStorage) TryAcquireLock(ctx context.Context, key string, value string, clusterName string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	existingEntry, exists := s.locks[key]

	if exists {
		// Check if the existing lock is expired
		if now.After(existingEntry.ExpiresAt) {
			// Lock is expired, can acquire
		} else {
			// Lock exists and is not expired
			if existingEntry.Value == value {
				// Lock is already held by the same value, effectively renew it
				existingEntry.UpdatedAt = now
				existingEntry.ExpiresAt = now.Add(ttl)
				existingEntry.TTL = ttl
				existingEntry.Version++ // Increment version
				s.locks[key] = existingEntry
				return true, nil
			}
			// Lock is held by someone else
			return false, nil
		}
	}

	// Acquire new lock or re-acquire expired lock
	newEntry := InMemoryLockEntry{
		LockMetadata: LockMetadata{
			Value:       value,
			ClusterName: clusterName,
			CreatedAt:   now,
			UpdatedAt:   now,
			ExpiresAt:   now.Add(ttl),
			TTL:         ttl,
			Version:     1, // Initial version
		},
	}
	s.locks[key] = newEntry
	return true, nil
}

// RenewLock attempts to renew a lock with the given key, value, and TTL.
func (s *InMemoryStorage) RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	existingEntry, exists := s.locks[key]

	if !exists {
		return false, nil // Lock does not exist
	}

	if existingEntry.Value != value {
		return false, nil // Lock held by someone else
	}

	if now.After(existingEntry.ExpiresAt) {
		return false, nil // Lock is expired, cannot renew
	}

	// Renew the lock
	existingEntry.UpdatedAt = now
	existingEntry.ExpiresAt = now.Add(ttl)
	existingEntry.TTL = ttl
	existingEntry.Version++ // Increment version
	s.locks[key] = existingEntry
	return true, nil
}

// ReleaseLock releases a lock with the given key and value.
func (s *InMemoryStorage) ReleaseLock(ctx context.Context, key string, value string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	existingEntry, exists := s.locks[key]
	if !exists {
		return false, nil // Lock does not exist, or already released
	}

	// Only the holder can release the lock
	// Note: We allow releasing an expired lock if it was held by this value.
	// If strict "only active lock can be released" is needed, add:
	// if time.Now().After(existingEntry.ExpiresAt) { return false, nil }
	if existingEntry.Value == value {
		delete(s.locks, key)
		return true, nil
	}

	return false, nil // Not the owner of the lock
}

// GetLockInfo gets the current metadata of a lock with the given key.
func (s *InMemoryStorage) GetLockInfo(ctx context.Context, key string) (*LockMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	entry, exists := s.locks[key]

	if !exists || now.After(entry.ExpiresAt) {
		return nil, nil // Lock does not exist or is expired
	}

	// Create a copy to return, update TTL
	metadata := entry.LockMetadata // This copies the struct
	metadata.TTL = time.Until(entry.ExpiresAt)
	if metadata.TTL < 0 { // Ensure TTL is not negative if there's a slight race
		metadata.TTL = 0
	}
	return &metadata, nil
}

// GetLockValue gets the current value of a lock with the given key.
func (s *InMemoryStorage) GetLockValue(ctx context.Context, key string) (string, error) {
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return "", err
	}
	if metadata == nil {
		return "", nil // Lock not found or expired
	}
	return metadata.Value, nil
}

// GetLockTTL gets the remaining TTL of a lock with the given key.
func (s *InMemoryStorage) GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return 0, err
	}
	if metadata == nil {
		return 0, nil // Lock not found or expired
	}
	return metadata.TTL, nil
}

// Close closes the storage connection. For in-memory, this can clear the locks.
func (s *InMemoryStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.locks = make(map[string]InMemoryLockEntry) // Clear all locks
	return nil
}
