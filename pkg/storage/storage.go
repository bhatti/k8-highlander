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

// This file defines the core storage interfaces and data structures for
// k8-highlander's distributed leader election mechanism. It establishes a
// storage-agnostic API that can be implemented by various backends (Redis,
// SQL databases, etc.) to provide consistent lock operations.
//
// The interface design ensures:
// - Abstraction of storage implementation details
// - Consistent lock semantics across storage backends
// - Rich metadata for lock tracking and monitoring
// - Support for atomic operations needed for reliable leader election
// - Clean separation between the leader election logic and storage mechanisms

package storage

import (
	"context"
	"time"
)

// LockMetadata contains additional information about a lock.
// It stores comprehensive details about lock ownership, timing, and versioning
// to support leader election, monitoring, and debugging.
type LockMetadata struct {
	// Value is the ID of the lock holder
	Value string `json:"value"`

	// ClusterName is the name of the cluster that holds the lock
	ClusterName string `json:"clusterName"`

	// CreatedAt is when the lock was first acquired
	CreatedAt time.Time `json:"createdAt"`

	// UpdatedAt is when the lock was last renewed
	UpdatedAt time.Time `json:"updatedAt"`

	// ExpiresAt is when the lock will expire if not renewed
	ExpiresAt time.Time `json:"expiresAt"`

	// TTL is the remaining time-to-live for the lock
	TTL     time.Duration `json:"ttl"`
	Version uint64        `json:"version"`
}

// LeaderStorage defines the interface for leader election storage
type LeaderStorage interface {
	// TryAcquireLock attempts to acquire a lock with the given key, value, and TTL
	// clusterName is the name of the cluster acquiring the lock
	// Returns true if successful, false otherwise
	TryAcquireLock(ctx context.Context, key string, value string, clusterName string, ttl time.Duration) (bool, error)

	// RenewLock attempts to renew a lock with the given key, value, and TTL
	// Returns true if successful, false otherwise
	RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error)

	// ReleaseLock releases a lock with the given key and value
	// Returns true if successful, false otherwise
	ReleaseLock(ctx context.Context, key string, value string) (bool, error)

	// GetLockInfo gets the current metadata of a lock with the given key
	// Returns the metadata and nil if successful, nil and error otherwise
	GetLockInfo(ctx context.Context, key string) (*LockMetadata, error)

	// GetLockValue gets the current value of a lock with the given key (for backward compatibility)
	// Returns the value and nil if successful, empty string and error otherwise
	GetLockValue(ctx context.Context, key string) (string, error)

	// GetLockTTL gets the remaining TTL of a lock with the given key (for backward compatibility)
	// Returns the TTL and nil if successful, 0 and error otherwise
	GetLockTTL(ctx context.Context, key string) (time.Duration, error)

	// Close closes the storage connection
	Close() error
}
