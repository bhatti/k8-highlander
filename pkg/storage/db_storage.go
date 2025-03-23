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

// This file implements a database-based storage backend for leader election locks
// using GORM as the ORM layer. It supports multiple SQL database types including
// PostgreSQL, MySQL, and SQLite.
//
// The implementation focuses on:
// - Reliable distributed locking using database transactions
// - Optimistic concurrency control with versioning
// - Automatic schema management and index optimization
// - Background cleanup of expired locks
// - Retry mechanisms for transient database errors
// - Cross-database compatibility
// - Performance optimizations for common operations
//
// This storage backend is suitable for environments where:
// - Redis is not available or not preferred
// - Persistent lock history is required
// - Integration with existing relational databases is desired
// - Strongest possible consistency guarantees are needed

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"k8s.io/klog/v2"
)

// LeaderLock represents a leader lock record in the database.
// It stores lock metadata, ownership information, and expiration details
// for the distributed leader election mechanism.
type LeaderLock struct {
	Key         string `gorm:"primaryKey"`
	Value       string
	ClusterName string
	Metadata    string    // JSON-encoded metadata
	CreatedAt   time.Time `gorm:"index"`
	UpdatedAt   time.Time
	ExpiresAt   time.Time `gorm:"index"`
	Version     uint64    // Version for optimistic concurrency control
}

// DBStorage implements the LeaderStorage interface using a relational database.
// It provides distributed lock operations using database transactions and
// optimistic concurrency control for consistency.
type DBStorage struct {
	db *gorm.DB
}

// NewDBStorage creates a new database storage implementation.
// It configures connection pooling, ensures the required schema exists,
// and starts a background cleanup routine for expired locks.
func NewDBStorage(db *gorm.DB) (*DBStorage, error) {
	// Create a storage instance
	storage := &DBStorage{
		db: db,
	}

	// Set connection pool settings
	sqlDB, err := db.DB()
	if err == nil {
		sqlDB.SetMaxIdleConns(10)
		sqlDB.SetMaxOpenConns(100)
		sqlDB.SetConnMaxLifetime(time.Hour)
	}

	// Ensure the schema is created
	if err := storage.ensureSchema(); err != nil {
		return nil, fmt.Errorf("failed to ensure schema: %w", err)
	}

	// Start a background goroutine to clean up expired locks
	go storage.startCleanupRoutine()

	return storage, nil
}

// startCleanupRoutine starts a background routine to clean up expired locks
func (s *DBStorage) startCleanupRoutine() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if err := s.cleanupExpiredLocks(); err != nil {
			klog.Errorf("Failed to clean up expired locks: %v", err)
		}
	}
}

// cleanupExpiredLocks removes expired locks from the database
func (s *DBStorage) cleanupExpiredLocks() error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		// Get the count of expired locks before deletion
		var count int64
		if err := tx.Model(&LeaderLock{}).Where("expires_at < ?", time.Now()).Count(&count).Error; err != nil {
			return fmt.Errorf("failed to count expired locks: %w", err)
		}

		if count > 0 {
			// Log the keys that will be deleted
			var expiredLocks []LeaderLock
			if err := tx.Where("expires_at < ?", time.Now()).Find(&expiredLocks).Error; err != nil {
				klog.Warningf("Failed to list expired locks: %v", err)
			} else {
				for _, lock := range expiredLocks {
					klog.Infof("Cleaning up expired lock: key=%s, value=%s, expired=%v",
						lock.Key, lock.Value, lock.ExpiresAt)
				}
			}
		}

		// Delete the expired locks
		result := tx.Where("expires_at < ?", time.Now()).Delete(&LeaderLock{})
		if result.Error != nil {
			return fmt.Errorf("failed to delete expired locks: %w", result.Error)
		}

		if result.RowsAffected > 0 {
			klog.V(4).Infof("Cleaned up %d expired locks", result.RowsAffected)
		}

		return nil
	})
}

// retryOnLockError retries a function if it encounters a database lock error
func (s *DBStorage) retryOnLockError(operation string, fn func() error) error {
	maxRetries := 5
	backoffFactor := 1.5
	initialBackoff := 10 * time.Millisecond

	var err error
	backoff := initialBackoff

	for i := 0; i < maxRetries; i++ {
		err = fn()

		// If no error or not a lock error, return immediately
		if err == nil || !isLockError(err) {
			return err
		}

		// Log the error and retry
		klog.V(4).Infof("Retrying %s due to lock error: %v (attempt %d/%d)",
			operation, err, i+1, maxRetries)

		// Wait before retrying
		time.Sleep(backoff)

		// Increase backoff for next retry
		backoff = time.Duration(float64(backoff) * backoffFactor)
	}

	// If we get here, all retries failed
	return fmt.Errorf("operation %s failed after %d retries: %w",
		operation, maxRetries, err)
}

// isLockError checks if an error is a database lock error
func isLockError(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	// SQLite lock errors
	return contains(errMsg, "database is locked") ||
		contains(errMsg, "database table is locked") ||
		contains(errMsg, "database schema is locked") ||
		contains(errMsg, "cannot commit transaction") ||
		// PostgreSQL lock errors
		contains(errMsg, "deadlock detected") ||
		contains(errMsg, "could not obtain lock") ||
		// MySQL lock errors
		contains(errMsg, "Lock wait timeout exceeded") ||
		contains(errMsg, "Deadlock found")
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return s != "" && substr != "" && s != substr && len(s) > len(substr) && s[len(s)-len(substr):] == substr
}

// TryAcquireLock attempts to acquire a lock with the given key, value, and TTL
func (s *DBStorage) TryAcquireLock(ctx context.Context, key string, value string, clusterName string, ttl time.Duration) (bool, error) {
	var success bool
	var opErr error
	dialect := s.db.Dialector.Name()

	err := s.retryOnLockError("TryAcquireLock", func() error {
		// CRITICAL FIX: First check if there's an existing lock that hasn't expired
		var existingLock LeaderLock
		result := s.db.WithContext(ctx).Where("key = ? AND expires_at > ?", key, time.Now()).First(&existingLock)

		if result.Error == nil {
			// Lock exists and is not expired
			if existingLock.Value != value {
				// Lock belongs to someone else
				klog.V(4).Infof("Lock %s already held by %s", key, existingLock.Value)
				success = false
				return nil
			}

			// Lock belongs to us, renew it
			now := time.Now()
			expiresAt := now.Add(ttl)

			updateResult := s.db.Model(&LeaderLock{}).
				Where("key = ? AND value = ?", key, value).
				Updates(map[string]interface{}{
					"updated_at": now,
					"expires_at": expiresAt,
					"version":    gorm.Expr("version + 1"),
				})

			if updateResult.Error != nil {
				return updateResult.Error
			}

			success = updateResult.RowsAffected > 0
			return nil
		} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			// Some other error occurred
			return result.Error
		}

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
			return err
		}

		// Lock doesn't exist or is expired, create a new one
		lock := LeaderLock{
			Key:         key,
			Value:       value,
			ClusterName: clusterName,
			Metadata:    string(metadataBytes),
			CreatedAt:   now,
			UpdatedAt:   now,
			ExpiresAt:   now.Add(ttl),
			Version:     version,
		}

		// Use a transaction to handle this properly for all database types
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// For SQLite, we need to delete any existing lock first
			if dialect == "sqlite" {
				if err := tx.Where("key = ?", key).Delete(&LeaderLock{}).Error; err != nil {
					return err
				}
			} else {
				// For other databases, we can use a more efficient approach
				// Delete expired locks for this key
				if err := tx.Where("key = ? AND expires_at <= ?", key, now).Delete(&LeaderLock{}).Error; err != nil {
					return err
				}

				// Check again if there's an active lock (to handle race conditions)
				var count int64
				if err := tx.Model(&LeaderLock{}).Where("key = ? AND expires_at > ?", key, now).Count(&count).Error; err != nil {
					return err
				}

				if count > 0 {
					// Someone else acquired the lock in the meantime
					success = false
					return nil
				}
			}

			// Create the new lock
			if err := tx.Create(&lock).Error; err != nil {
				// Check for unique constraint violation
				if strings.Contains(err.Error(), "unique constraint") ||
					strings.Contains(err.Error(), "duplicate key") {
					// Someone else acquired the lock in the meantime
					success = false
					return nil
				}
				return err
			}

			success = true
			return nil
		})

		if err != nil {
			return err
		}

		// CRITICAL FIX: Verify the lock was created with our value
		if success {
			var verifyLock LeaderLock
			verifyResult := s.db.WithContext(ctx).Where("key = ?", key).First(&verifyLock)
			if verifyResult.Error != nil {
				klog.Warningf("Failed to verify lock creation: %v", verifyResult.Error)
				success = false
				return nil
			}

			if verifyLock.Value != value {
				klog.Warningf("Lock verification failed! Expected value %s, got %s", value, verifyLock.Value)
				success = false
				return nil
			}
		}

		return nil
	})

	if err != nil {
		return false, err
	}

	if success {
		klog.V(4).Infof("Acquired lock %s for %s on cluster %s, expires in %v",
			key, value, clusterName, ttl)
	}

	return success, opErr
}

// RenewLock attempts to renew a lock with the given key, value, and TTL
func (s *DBStorage) RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	var success bool
	var opErr error

	err := s.retryOnLockError("RenewLock", func() error {
		// Use a transaction to ensure atomicity
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Check if the lock exists and belongs to us
			var existingLock LeaderLock
			result := tx.Where("key = ? AND value = ?", key, value).First(&existingLock)
			if result.Error != nil {
				if errors.Is(result.Error, gorm.ErrRecordNotFound) {
					success = false
					return nil // Lock doesn't exist or doesn't belong to us
				}
				return result.Error
			}

			// Parse existing metadata
			var metadata LockMetadata
			if err := json.Unmarshal([]byte(existingLock.Metadata), &metadata); err != nil {
				// If we can't unmarshal, create new metadata
				now := time.Now()
				metadata = LockMetadata{
					Value:       existingLock.Value,
					ClusterName: existingLock.ClusterName,
					CreatedAt:   existingLock.CreatedAt,
					UpdatedAt:   now,
					ExpiresAt:   now.Add(ttl),
					TTL:         ttl,
					Version:     existingLock.Version,
				}
			}

			// Increment version for optimistic concurrency control
			newVersion := metadata.Version + 1

			// Update metadata
			now := time.Now()
			metadata.UpdatedAt = now
			metadata.ExpiresAt = now.Add(ttl)
			metadata.TTL = ttl
			metadata.Version = newVersion

			// Serialize updated metadata
			metadataBytes, err := json.Marshal(metadata)
			if err != nil {
				return err
			}

			// Update the lock record with optimistic locking
			result = tx.Model(&LeaderLock{}).
				Where("key = ? AND value = ? AND version = ?", key, value, existingLock.Version).
				Updates(map[string]interface{}{
					"updated_at": now,
					"expires_at": now.Add(ttl),
					"metadata":   string(metadataBytes),
					"version":    newVersion,
				})

			if result.Error != nil {
				return result.Error
			}

			// Check if the update was successful (affected rows > 0)
			if result.RowsAffected == 0 {
				klog.Warningf("Failed to renew lock %s: version conflict (expected %d)",
					key, existingLock.Version)
				success = false
				return nil
			}

			// CRITICAL FIX: Verify the lock was renewed correctly
			var verifyLock LeaderLock
			verifyResult := tx.Where("key = ?", key).First(&verifyLock)
			if verifyResult.Error != nil {
				klog.Warningf("Failed to verify lock renewal: %v", verifyResult.Error)
				success = false
				return nil
			}

			if verifyLock.Value != value {
				klog.Warningf("Lock renewal verification failed! Expected value %s, got %s",
					value, verifyLock.Value)
				success = false
				return nil
			}

			success = true
			return nil
		})

		opErr = err
		return err
	})

	if err != nil {
		return false, err
	}

	if success {
		klog.V(4).Infof("Renewed lock %s for %s, expires in %v", key, value, ttl)
	}

	return success, opErr
}

// ReleaseLock releases a lock with the given key and value
func (s *DBStorage) ReleaseLock(ctx context.Context, key string, value string) (bool, error) {
	var success bool
	var opErr error

	err := s.retryOnLockError("ReleaseLock", func() error {
		// Use a transaction to ensure atomicity
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Delete the lock only if it belongs to us
			result := tx.Where("key = ? AND value = ?", key, value).Delete(&LeaderLock{})
			if result.Error != nil {
				return result.Error
			}

			success = result.RowsAffected > 0

			// CRITICAL FIX: Verify the lock is actually gone
			if success {
				var count int64
				if err := tx.Model(&LeaderLock{}).Where("key = ?", key).Count(&count).Error; err != nil {
					return err
				}

				if count > 0 {
					klog.Warningf("Lock still exists after deletion! Key: %s", key)
					// Force delete the key
					if err := tx.Where("key = ?", key).Delete(&LeaderLock{}).Error; err != nil {
						return err
					}
				}
			}

			return nil
		})

		opErr = err
		return err
	})

	if err != nil {
		return false, err
	}

	if success {
		klog.V(4).Infof("Released lock %s for %s", key, value)
	}

	return success, opErr
}

// GetLockInfo gets the current metadata of a lock with the given key
func (s *DBStorage) GetLockInfo(ctx context.Context, key string) (*LockMetadata, error) {
	var lock LeaderLock
	var opErr error

	err := s.retryOnLockError("GetLockInfo", func() error {
		// Use the composite index on key and expires_at
		result := s.db.WithContext(ctx).
			Where("key = ? AND expires_at > ?", key, time.Now()).
			First(&lock)

		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil // Lock doesn't exist or is expired
			}
			return result.Error
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	// If no lock was found
	if lock.Key == "" {
		return nil, nil
	}

	// Try to parse metadata
	var metadata LockMetadata
	if err := json.Unmarshal([]byte(lock.Metadata), &metadata); err != nil {
		// If we can't unmarshal, create metadata from the lock record
		metadata = LockMetadata{
			Value:       lock.Value,
			ClusterName: lock.ClusterName,
			CreatedAt:   lock.CreatedAt,
			UpdatedAt:   lock.UpdatedAt,
			ExpiresAt:   lock.ExpiresAt,
			TTL:         time.Until(lock.ExpiresAt),
			Version:     lock.Version,
		}
	} else {
		// Update TTL based on current time
		metadata.TTL = time.Until(lock.ExpiresAt)
		metadata.ExpiresAt = lock.ExpiresAt
		metadata.Version = lock.Version
	}

	return &metadata, opErr
}

// GetLockValue gets the current value of a lock with the given key (for backward compatibility)
func (s *DBStorage) GetLockValue(ctx context.Context, key string) (string, error) {
	start := time.Now()
	defer func() {
		klog.V(4).Infof("GetLockValue took %v", time.Since(start))
	}()
	var lock LeaderLock
	var opErr error

	err := s.retryOnLockError("GetLockValue", func() error {
		// Use the composite index and only select the value column for efficiency
		result := s.db.WithContext(ctx).
			Select("value").
			Where("key = ? AND expires_at > ?", key, time.Now()).
			First(&lock)

		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil // Lock doesn't exist or is expired
			}
			return result.Error
		}
		return nil
	})

	if err != nil {
		return "", err
	}

	// If no lock was found
	if lock.Value == "" {
		return "", nil
	}

	return lock.Value, opErr
}

// GetLockTTL gets the remaining TTL of a lock with the given key (for backward compatibility)
func (s *DBStorage) GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	var lock LeaderLock
	var opErr error

	err := s.retryOnLockError("GetLockTTL", func() error {
		// Use the composite index and only select the expires_at column for efficiency
		result := s.db.WithContext(ctx).
			Select("expires_at").
			Where("key = ? AND expires_at > ?", key, time.Now()).
			First(&lock)

		if result.Error != nil {
			if errors.Is(result.Error, gorm.ErrRecordNotFound) {
				return nil // Lock doesn't exist or is expired
			}
			return result.Error
		}
		return nil
	})

	if err != nil {
		return 0, err
	}

	// If no lock was found
	if lock.ExpiresAt.IsZero() {
		return 0, nil
	}

	return time.Until(lock.ExpiresAt), opErr
}

// Close closes the database connection
func (s *DBStorage) Close() error {
	// GORM doesn't have a close method for the DB object
	// If using a specific database driver, you might need to close the underlying connection
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// ensureSchema ensures the database schema is created
func (s *DBStorage) ensureSchema() error {
	// Get the database dialect
	dialect := s.db.Dialector.Name()
	klog.Infof("Using database dialect: %s", dialect)

	// Check if the table exists using a dialect-specific approach
	var tableExists bool

	if dialect == "sqlite" {
		// SQLite-specific check
		var result string
		err := s.db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name='leader_locks'").Scan(&result).Error
		if err != nil {
			return fmt.Errorf("failed to check if table exists in SQLite: %w", err)
		}
		tableExists = result != ""
	} else if dialect == "postgres" {
		// PostgreSQL-specific check
		var count int64
		err := s.db.Raw("SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'leader_locks' AND table_schema = current_schema()").Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check if table exists in PostgreSQL: %w", err)
		}
		tableExists = count > 0
	} else {
		// Generic approach for other databases - let GORM handle it
		tableExists = s.db.Migrator().HasTable(&LeaderLock{})
	}

	if !tableExists {
		// Create the table using GORM's AutoMigrate
		klog.Infof("Creating leader_locks table")
		if err := s.db.AutoMigrate(&LeaderLock{}); err != nil {
			return fmt.Errorf("failed to create leader_locks table: %w", err)
		}
	} else {
		klog.Infof("Leader locks table already exists")
	}

	// Check and create indexes regardless of whether the table was just created
	// This ensures indexes exist even if the table was created without them

	// Check if the expires_at index exists
	var indexExists bool

	if dialect == "sqlite" {
		var result string
		err := s.db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_leader_locks_expires_at'").Scan(&result).Error
		if err != nil {
			return fmt.Errorf("failed to check if index exists in SQLite: %w", err)
		}
		indexExists = result != ""
	} else if dialect == "postgres" {
		var count int64
		err := s.db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_leader_locks_expires_at'").Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check if index exists in PostgreSQL: %w", err)
		}
		indexExists = count > 0
	} else {
		// For other databases, assume we need to create the index
		indexExists = false
	}

	// Create the expires_at index if it doesn't exist
	if !indexExists {
		klog.Infof("Creating index on expires_at column")
		indexSQL := "CREATE INDEX IF NOT EXISTS idx_leader_locks_expires_at ON leader_locks(expires_at)"
		if err := s.db.Exec(indexSQL).Error; err != nil {
			return fmt.Errorf("failed to create expires_at index: %w", err)
		}
	} else {
		klog.Infof("Index on expires_at column already exists")
	}

	// Create a composite index on key and expires_at for our most common query
	var compositeIndexExists bool

	if dialect == "sqlite" {
		var result string
		err := s.db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_leader_locks_key_expires_at'").Scan(&result).Error
		if err != nil {
			return fmt.Errorf("failed to check if composite index exists in SQLite: %w", err)
		}
		compositeIndexExists = result != ""
	} else if dialect == "postgres" {
		var count int64
		err := s.db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_leader_locks_key_expires_at'").Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check if composite index exists in PostgreSQL: %w", err)
		}
		compositeIndexExists = count > 0
	} else {
		// For other databases, assume we need to create the index
		compositeIndexExists = false
	}

	// Create the composite index if it doesn't exist
	if !compositeIndexExists {
		klog.Infof("Creating composite index on key and expires_at columns")
		compositeIndexSQL := "CREATE INDEX IF NOT EXISTS idx_leader_locks_key_expires_at ON leader_locks(key, expires_at)"
		if err := s.db.Exec(compositeIndexSQL).Error; err != nil {
			return fmt.Errorf("failed to create composite index: %w", err)
		}
	} else {
		klog.Infof("Composite index on key and expires_at columns already exists")
	}

	// Create an index on the value column for queries that filter by value
	var valueIndexExists bool

	if dialect == "sqlite" {
		var result string
		err := s.db.Raw("SELECT name FROM sqlite_master WHERE type='index' AND name='idx_leader_locks_value'").Scan(&result).Error
		if err != nil {
			return fmt.Errorf("failed to check if value index exists in SQLite: %w", err)
		}
		valueIndexExists = result != ""
	} else if dialect == "postgres" {
		var count int64
		err := s.db.Raw("SELECT COUNT(*) FROM pg_indexes WHERE indexname = 'idx_leader_locks_value'").Count(&count).Error
		if err != nil {
			return fmt.Errorf("failed to check if value index exists in PostgreSQL: %w", err)
		}
		valueIndexExists = count > 0
	} else {
		// For other databases, assume we need to create the index
		valueIndexExists = false
	}

	// Create the value index if it doesn't exist
	if !valueIndexExists {
		klog.Infof("Creating index on value column")
		valueIndexSQL := "CREATE INDEX IF NOT EXISTS idx_leader_locks_value ON leader_locks(value)"
		if err := s.db.Exec(valueIndexSQL).Error; err != nil {
			return fmt.Errorf("failed to create value index: %w", err)
		}
	} else {
		klog.Infof("Index on value column already exists")
	}

	klog.Infof("Successfully ensured leader_locks table and indexes")
	return nil
}
