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
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/grpc/codes"
	"k8s.io/klog/v2"
)

const (
	leaderLocksTable = "LeaderLocks"
	lockKeyCol       = "LockKey"
	lockValueCol     = "LockValue"
	clusterNameCol   = "ClusterName"
	metadataJsonCol  = "MetadataJson"
	createdAtCol     = "CreatedAt"
	updatedAtCol     = "UpdatedAt"
	expiresAtCol     = "ExpiresAt"
	lockVersionCol   = "LockVersion"
)

// SpannerStorage implements the LeaderStorage interface using Google Cloud Spanner.
type SpannerStorage struct {
	client *spanner.Client
}

// NewSpannerStorage creates a new Spanner storage implementation.
func NewSpannerStorage(client *spanner.Client) (*SpannerStorage, error) {
	storage := &SpannerStorage{client: client}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := storage.ensureSchema(ctx); err != nil {
		klog.Warningf("Failed to ensure schema automatically: %v", err)
		klog.Warningf("Please ensure the Spanner table exists manually")
		// Don't fail here - allow the storage to be created and let it fail later if table doesn't exist
	}
	go storage.startCleanupRoutine()
	return storage, nil
}

func (s *SpannerStorage) ensureSchema(ctx context.Context) error {
	// Check if table exists by trying to query it
	stmt := spanner.NewStatement(fmt.Sprintf(`
		SELECT COUNT(*) as count FROM %s WHERE FALSE
	`, leaderLocksTable))

	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	tableExists := true
	err := iter.Do(func(row *spanner.Row) error {
		// If we can execute this query, the table exists
		return nil
	})

	if err != nil {
		// If we get an error, likely the table doesn't exist
		if spanner.ErrCode(err) == codes.NotFound ||
			strings.Contains(err.Error(), "not found") ||
			strings.Contains(err.Error(), "does not exist") {
			tableExists = false
		} else {
			return fmt.Errorf("failed to check if table exists: %w", err)
		}
	}

	if !tableExists {
		klog.Warningf("Spanner table %s does not exist.", leaderLocksTable)
		klog.Warningf("Please create the table manually using the following DDL:")
		klog.Warningf("")
		klog.Warningf("CREATE TABLE %s (", leaderLocksTable)
		klog.Warningf("  %s STRING(MAX) NOT NULL,", lockKeyCol)
		klog.Warningf("  %s STRING(MAX) NOT NULL,", lockValueCol)
		klog.Warningf("  %s STRING(MAX) NOT NULL,", clusterNameCol)
		klog.Warningf("  %s STRING(MAX),", metadataJsonCol)
		klog.Warningf("  %s TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),", createdAtCol)
		klog.Warningf("  %s TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),", updatedAtCol)
		klog.Warningf("  %s TIMESTAMP NOT NULL,", expiresAtCol)
		klog.Warningf("  %s INT64 NOT NULL", lockVersionCol)
		klog.Warningf(") PRIMARY KEY (%s);", lockKeyCol)
		klog.Warningf("")
		klog.Warningf("CREATE INDEX IdxLeaderLocksExpiresAt ON %s (%s);", leaderLocksTable, expiresAtCol)
		klog.Warningf("CREATE INDEX IdxLeaderLocksLockValue ON %s (%s);", leaderLocksTable, lockValueCol)
		klog.Warningf("")

		return fmt.Errorf("table %s does not exist - please create it manually using the DDL above", leaderLocksTable)
	}

	klog.Infof("Spanner table %s exists and is accessible.", leaderLocksTable)
	return nil
}

func (s *SpannerStorage) startCleanupRoutine() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
		if err := s.cleanupExpiredLocks(ctx); err != nil {
			klog.Errorf("Spanner: Failed to clean up expired locks: %v", err)
		}
		cancel()
	}
}

func (s *SpannerStorage) cleanupExpiredLocks(ctx context.Context) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		stmt := spanner.NewStatement(fmt.Sprintf("DELETE FROM %s WHERE %s < CURRENT_TIMESTAMP()", leaderLocksTable, expiresAtCol))
		rowCount, err := txn.Update(ctx, stmt)
		if err != nil {
			return err
		}
		if rowCount > 0 {
			klog.V(4).Infof("Spanner: Cleaned up %d expired locks", rowCount)
		}
		return nil
	})
	return err
}

func (s *SpannerStorage) TryAcquireLock(ctx context.Context, key string, value string, clusterName string, ttl time.Duration) (bool, error) {
	var acquired bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// Check if lock exists and is not expired
		stmt := spanner.NewStatement(fmt.Sprintf(`
			SELECT %s, %s, %s, %s, %s, %s 
			FROM %s 
			WHERE %s = @key AND %s > CURRENT_TIMESTAMP()
		`, lockValueCol, clusterNameCol, expiresAtCol, createdAtCol, lockVersionCol, metadataJsonCol,
			leaderLocksTable, lockKeyCol, expiresAtCol))
		stmt.Params["key"] = key

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		var existingLock *struct {
			Value       string
			ClusterName string
			ExpiresAt   time.Time
			CreatedAt   time.Time
			Version     int64
			Metadata    string
		}

		err := iter.Do(func(row *spanner.Row) error {
			existingLock = &struct {
				Value       string
				ClusterName string
				ExpiresAt   time.Time
				CreatedAt   time.Time
				Version     int64
				Metadata    string
			}{}

			if err := row.ColumnByName(lockValueCol, &existingLock.Value); err != nil {
				return fmt.Errorf("reading %s: %w", lockValueCol, err)
			}
			if err := row.ColumnByName(clusterNameCol, &existingLock.ClusterName); err != nil {
				return fmt.Errorf("reading %s: %w", clusterNameCol, err)
			}
			if err := row.ColumnByName(expiresAtCol, &existingLock.ExpiresAt); err != nil {
				return fmt.Errorf("reading %s: %w", expiresAtCol, err)
			}
			if err := row.ColumnByName(createdAtCol, &existingLock.CreatedAt); err != nil {
				return fmt.Errorf("reading %s: %w", createdAtCol, err)
			}
			if err := row.ColumnByName(lockVersionCol, &existingLock.Version); err != nil {
				return fmt.Errorf("reading %s: %w", lockVersionCol, err)
			}
			// Handle nullable metadata column
			var metadataStr spanner.NullString
			if err := row.ColumnByName(metadataJsonCol, &metadataStr); err != nil {
				return fmt.Errorf("reading %s: %w", metadataJsonCol, err)
			}
			if metadataStr.Valid {
				existingLock.Metadata = metadataStr.StringVal
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to query existing lock: %w", err)
		}

		now := time.Now()
		newExpiresAt := now.Add(ttl)

		if existingLock != nil {
			// Lock exists and is not expired
			if existingLock.Value == value {
				// We own the lock, renew it
				klog.V(4).Infof("Spanner: TryAcquireLock renewing existing lock %s for %s", key, value)

				// Create updated metadata
				newMeta := LockMetadata{
					Value:       value,
					ClusterName: clusterName,
					CreatedAt:   existingLock.CreatedAt, // Keep original creation time
					UpdatedAt:   now,
					ExpiresAt:   newExpiresAt,
					TTL:         ttl,
					Version:     uint64(existingLock.Version + 1),
				}

				metadataBytes, err := json.Marshal(newMeta)
				if err != nil {
					return fmt.Errorf("failed to marshal metadata: %w", err)
				}

				mutation := spanner.UpdateMap(leaderLocksTable, map[string]interface{}{
					lockKeyCol:      key,
					lockValueCol:    value,
					clusterNameCol:  clusterName,
					metadataJsonCol: string(metadataBytes),
					updatedAtCol:    spanner.CommitTimestamp,
					expiresAtCol:    newExpiresAt,
					lockVersionCol:  int64(newMeta.Version),
				})
				txn.BufferWrite([]*spanner.Mutation{mutation})
				acquired = true
			} else {
				// Lock is owned by someone else
				klog.V(4).Infof("Spanner: TryAcquireLock failed, lock %s is held by %s", key, existingLock.Value)
				acquired = false
			}
		} else {
			// No active lock exists, create new one
			klog.V(4).Infof("Spanner: TryAcquireLock creating new lock %s for %s", key, value)

			// First, clean up any expired locks for this key
			deleteStmt := spanner.NewStatement(fmt.Sprintf(
				"DELETE FROM %s WHERE %s = @key AND %s <= CURRENT_TIMESTAMP()",
				leaderLocksTable, lockKeyCol, expiresAtCol))
			deleteStmt.Params["key"] = key
			_, err := txn.Update(ctx, deleteStmt)
			if err != nil {
				return fmt.Errorf("failed to cleanup expired locks: %w", err)
			}

			// Create new metadata
			newMeta := LockMetadata{
				Value:       value,
				ClusterName: clusterName,
				CreatedAt:   now,
				UpdatedAt:   now,
				ExpiresAt:   newExpiresAt,
				TTL:         ttl,
				Version:     uint64(now.UnixNano()),
			}

			metadataBytes, err := json.Marshal(newMeta)
			if err != nil {
				return fmt.Errorf("failed to marshal metadata: %w", err)
			}

			mutation := spanner.InsertMap(leaderLocksTable, map[string]interface{}{
				lockKeyCol:      key,
				lockValueCol:    value,
				clusterNameCol:  clusterName,
				metadataJsonCol: string(metadataBytes),
				createdAtCol:    spanner.CommitTimestamp,
				updatedAtCol:    spanner.CommitTimestamp,
				expiresAtCol:    newExpiresAt,
				lockVersionCol:  int64(newMeta.Version),
			})
			txn.BufferWrite([]*spanner.Mutation{mutation})
			acquired = true
		}

		return nil
	})

	if err != nil {
		return false, fmt.Errorf("transaction failed: %w", err)
	}

	if acquired {
		klog.V(4).Infof("Spanner: Successfully acquired lock %s for %s on cluster %s", key, value, clusterName)
	}

	return acquired, nil
}

func (s *SpannerStorage) RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	var renewed bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// Check if we own the lock and it's not expired
		stmt := spanner.NewStatement(fmt.Sprintf(`
			SELECT %s, %s, %s, %s, %s, %s 
			FROM %s 
			WHERE %s = @key AND %s = @value AND %s > CURRENT_TIMESTAMP()
		`, lockValueCol, clusterNameCol, expiresAtCol, createdAtCol, lockVersionCol, metadataJsonCol,
			leaderLocksTable, lockKeyCol, lockValueCol, expiresAtCol))
		stmt.Params["key"] = key
		stmt.Params["value"] = value

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		var existingLock *struct {
			Value       string
			ClusterName string
			ExpiresAt   time.Time
			CreatedAt   time.Time
			Version     int64
			Metadata    string
		}

		err := iter.Do(func(row *spanner.Row) error {
			existingLock = &struct {
				Value       string
				ClusterName string
				ExpiresAt   time.Time
				CreatedAt   time.Time
				Version     int64
				Metadata    string
			}{}

			if err := row.ColumnByName(lockValueCol, &existingLock.Value); err != nil {
				return fmt.Errorf("reading %s: %w", lockValueCol, err)
			}
			if err := row.ColumnByName(clusterNameCol, &existingLock.ClusterName); err != nil {
				return fmt.Errorf("reading %s: %w", clusterNameCol, err)
			}
			if err := row.ColumnByName(expiresAtCol, &existingLock.ExpiresAt); err != nil {
				return fmt.Errorf("reading %s: %w", expiresAtCol, err)
			}
			if err := row.ColumnByName(createdAtCol, &existingLock.CreatedAt); err != nil {
				return fmt.Errorf("reading %s: %w", createdAtCol, err)
			}
			if err := row.ColumnByName(lockVersionCol, &existingLock.Version); err != nil {
				return fmt.Errorf("reading %s: %w", lockVersionCol, err)
			}
			// Handle nullable metadata column
			var metadataStr spanner.NullString
			if err := row.ColumnByName(metadataJsonCol, &metadataStr); err != nil {
				return fmt.Errorf("reading %s: %w", metadataJsonCol, err)
			}
			if metadataStr.Valid {
				existingLock.Metadata = metadataStr.StringVal
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("failed to query lock for renewal: %w", err)
		}

		if existingLock == nil {
			klog.V(4).Infof("Spanner: RenewLock failed, lock %s not found or expired", key)
			renewed = false
			return nil
		}

		// Renew the lock
		now := time.Now()
		newExpiresAt := now.Add(ttl)

		// Create updated metadata
		newMeta := LockMetadata{
			Value:       value,
			ClusterName: existingLock.ClusterName,
			CreatedAt:   existingLock.CreatedAt, // Keep original creation time
			UpdatedAt:   now,
			ExpiresAt:   newExpiresAt,
			TTL:         ttl,
			Version:     uint64(existingLock.Version + 1),
		}

		metadataBytes, err := json.Marshal(newMeta)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		mutation := spanner.UpdateMap(leaderLocksTable, map[string]interface{}{
			lockKeyCol:      key,
			metadataJsonCol: string(metadataBytes),
			updatedAtCol:    spanner.CommitTimestamp,
			expiresAtCol:    newExpiresAt,
			lockVersionCol:  int64(newMeta.Version),
		})
		txn.BufferWrite([]*spanner.Mutation{mutation})
		renewed = true
		klog.V(4).Infof("Spanner: Renewed lock %s for %s", key, value)
		return nil
	})

	return renewed, err
}

func (s *SpannerStorage) ReleaseLock(ctx context.Context, key string, value string) (bool, error) {
	var released bool
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// Verify we own the lock before deleting
		stmt := spanner.NewStatement(fmt.Sprintf(`
			SELECT %s FROM %s WHERE %s = @key
		`, lockValueCol, leaderLocksTable, lockKeyCol))
		stmt.Params["key"] = key

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		var currentValue string
		err := iter.Do(func(row *spanner.Row) error {
			return row.ColumnByName(lockValueCol, &currentValue)
		})

		if err != nil {
			return fmt.Errorf("failed to query lock for release: %w", err)
		}

		if currentValue == "" {
			klog.V(4).Infof("Spanner: ReleaseLock failed, lock %s not found", key)
			released = false
			return nil
		}

		if currentValue != value {
			klog.V(4).Infof("Spanner: ReleaseLock failed, lock %s not owned by %s (owner: %s)", key, value, currentValue)
			released = false
			return nil
		}

		// Delete the lock
		mutation := spanner.Delete(leaderLocksTable, spanner.Key{key})
		txn.BufferWrite([]*spanner.Mutation{mutation})
		released = true
		klog.V(4).Infof("Spanner: Released lock %s for %s", key, value)
		return nil
	})

	return released, err
}

func (s *SpannerStorage) GetLockInfo(ctx context.Context, key string) (*LockMetadata, error) {
	stmt := spanner.NewStatement(fmt.Sprintf(`
		SELECT %s, %s, %s, %s, %s, %s 
		FROM %s 
		WHERE %s = @key AND %s > CURRENT_TIMESTAMP()
	`, lockValueCol, clusterNameCol, expiresAtCol, createdAtCol, updatedAtCol, lockVersionCol,
		leaderLocksTable, lockKeyCol, expiresAtCol))
	stmt.Params["key"] = key

	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var metadata *LockMetadata
	err := iter.Do(func(row *spanner.Row) error {
		var lockVal, clusterName string
		var createdAt, updatedAt, expiresAt time.Time
		var version int64

		if err := row.ColumnByName(lockValueCol, &lockVal); err != nil {
			return fmt.Errorf("reading %s: %w", lockValueCol, err)
		}
		if err := row.ColumnByName(clusterNameCol, &clusterName); err != nil {
			return fmt.Errorf("reading %s: %w", clusterNameCol, err)
		}
		if err := row.ColumnByName(expiresAtCol, &expiresAt); err != nil {
			return fmt.Errorf("reading %s: %w", expiresAtCol, err)
		}
		if err := row.ColumnByName(createdAtCol, &createdAt); err != nil {
			return fmt.Errorf("reading %s: %w", createdAtCol, err)
		}
		if err := row.ColumnByName(updatedAtCol, &updatedAt); err != nil {
			return fmt.Errorf("reading %s: %w", updatedAtCol, err)
		}
		if err := row.ColumnByName(lockVersionCol, &version); err != nil {
			return fmt.Errorf("reading %s: %w", lockVersionCol, err)
		}

		ttl := time.Until(expiresAt)
		if ttl < 0 {
			ttl = 0
		}

		metadata = &LockMetadata{
			Value:       lockVal,
			ClusterName: clusterName,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			ExpiresAt:   expiresAt,
			TTL:         ttl,
			Version:     uint64(version),
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to get lock info: %w", err)
	}

	return metadata, nil
}

func (s *SpannerStorage) GetLockValue(ctx context.Context, key string) (string, error) {
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return "", err
	}
	if metadata == nil {
		return "", nil
	}
	return metadata.Value, nil
}

func (s *SpannerStorage) GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	metadata, err := s.GetLockInfo(ctx, key)
	if err != nil {
		return 0, err
	}
	if metadata == nil {
		return 0, nil
	}
	return metadata.TTL, nil
}

func (s *SpannerStorage) Close() error {
	s.client.Close()
	klog.Info("Spanner client closed.")
	return nil
}
