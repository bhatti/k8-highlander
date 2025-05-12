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

// Package storage provides abstracted storage interfaces and implementations for
// leader election locks. This package implements the factory pattern to create
// appropriate storage backends based on configuration.
//
// The storage layer supports multiple backend types:
// - Redis: For high-performance, low-latency lock management suitable for most deployments
// - Database (SQL): For environments where Redis is not available or persistent storage is preferred
//
// The storage implementation handles distributed lock operations including:
// - Lock acquisition with TTL to prevent split-brain scenarios
// - Lock renewal for continued leadership
// - Atomic lock releases to ensure proper handover
// - Lock state inspection for monitoring

package storage

import (
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
)

// NewLeaderStorage creates a new leader storage instance based on the provided configuration.
// It implements a factory pattern to instantiate the appropriate storage implementation
// based on the configuration type.
//
// Supported storage types:
// - Redis: Uses Redis for lock management with SETNX and expiration
// - Database: Uses a relational database for lock management with transactions
//
// Parameters:
//   - config: Storage configuration containing type and client connections
//
// Returns:
//   - LeaderStorage: Initialized storage implementation
//   - error: Error if configuration is invalid or initialization fails
func NewLeaderStorage(config *common.StorageConfig) (LeaderStorage, error) {
	switch config.Type {
	case common.StorageTypeRedis:
		if config.RedisClient == nil {
			return nil, fmt.Errorf("redis client is required for redis storage")
		}
		return NewRedisStorage(config.RedisClient), nil
	case common.StorageTypeDB:
		if config.DBClient == nil {
			return nil, fmt.Errorf("database client is required for db storage")
		}
		return NewDBStorage(config.DBClient)
	case common.StorageTypeMemory:
		return NewInMemoryStorage(), nil
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", config.Type)
	}
}
