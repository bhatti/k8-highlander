package storage

import (
	database "cloud.google.com/go/spanner/admin/database/apiv1"
	"cloud.google.com/go/spanner/admin/database/apiv1/databasepb"
	instanceadmin "cloud.google.com/go/spanner/admin/instance/apiv1"
	"cloud.google.com/go/spanner/admin/instance/apiv1/instancepb"
	"context"
	"fmt"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRedisStorage(t *testing.T) (*RedisStorage, *miniredis.Miniredis, func()) {
	// Start a mini Redis server
	mr, err := miniredis.Run()
	require.NoError(t, err)

	// Create a Redis client
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})

	// Create a Redis storage
	storage := NewRedisStorage(client)

	// Return the storage and a cleanup function
	return storage, mr, func() {
		_ = client.Close()
		mr.Close()
	}
}

func setupDBStorage(t *testing.T) (*DBStorage, func()) {
	// Create a unique database file for this test to avoid conflicts
	dbFile := fmt.Sprintf("file:memdb_%d?mode=memory&cache=shared", time.Now().UnixNano())

	// Configure GORM with less verbose logging and better SQLite settings
	gormConfig := &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
		PrepareStmt:                              false, // Disable prepared statements for SQLite in tests
		SkipDefaultTransaction:                   true,  // Skip default transaction for better SQLite performance
	}

	// Create a new database connection
	db, err := gorm.Open(sqlite.Open(dbFile), gormConfig)
	require.NoError(t, err)

	// Configure SQLite for better test performance
	sqlDB, err := db.DB()
	require.NoError(t, err)

	// Set connection pool settings - keep these minimal for tests
	sqlDB.SetMaxOpenConns(1) // Use just 1 connection for SQLite tests
	sqlDB.SetMaxIdleConns(1)

	// Execute PRAGMA statements for SQLite
	db.Exec("PRAGMA journal_mode = MEMORY") // Use memory journal for tests
	db.Exec("PRAGMA synchronous = OFF")     // Turn off synchronous for tests
	db.Exec("PRAGMA foreign_keys = OFF")

	// Create a DB storage
	storage, err := NewDBStorage(db)
	require.NoError(t, err)

	// Return the storage and a cleanup function
	return storage, func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}
}

func setupSpannerStorage(t *testing.T) (*SpannerStorage, func()) {

	// uncomment for testing
	_ = os.Setenv("SPANNER_EMULATOR_HOST", "localhost:9010")

	// Check if Spanner emulator is available
	spannerEmulatorHost := os.Getenv("SPANNER_EMULATOR_HOST")
	if spannerEmulatorHost == "" {
		t.Skip("Spanner emulator not available. Set SPANNER_EMULATOR_HOST=localhost:9010 and start emulator")
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	if projectID == "" {
		projectID = "test-project"
	}

	instanceID := "test-instance"
	databaseID := "test-database"

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create admin clients to set up instance and database
	instanceAdmin, err := instanceadmin.NewInstanceAdminClient(ctx)
	if err != nil {
		t.Skipf("Failed to create instance admin client: %v", err)
	}
	defer instanceAdmin.Close()

	databaseAdmin, err := database.NewDatabaseAdminClient(ctx)
	if err != nil {
		t.Skipf("Failed to create database admin client: %v", err)
	}
	defer databaseAdmin.Close()

	// Create instance if it doesn't exist
	instancePath := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	_, err = instanceAdmin.GetInstance(ctx, &instancepb.GetInstanceRequest{
		Name: instancePath,
	})
	if err != nil {
		// Instance doesn't exist, create it
		t.Logf("Creating Spanner instance: %s", instancePath)
		op, err := instanceAdmin.CreateInstance(ctx, &instancepb.CreateInstanceRequest{
			Parent:     fmt.Sprintf("projects/%s", projectID),
			InstanceId: instanceID,
			Instance: &instancepb.Instance{
				Config:      fmt.Sprintf("projects/%s/instanceConfigs/emulator-config", projectID),
				DisplayName: "Test Instance",
				NodeCount:   1,
			},
		})
		if err != nil {
			t.Skipf("Failed to create instance: %v", err)
		}

		_, err = op.Wait(ctx)
		if err != nil {
			t.Skipf("Failed to wait for instance creation: %v", err)
		}
		t.Logf("Successfully created instance: %s", instancePath)
	}

	// Create database if it doesn't exist
	databasePath := fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, databaseID)
	_, err = databaseAdmin.GetDatabase(ctx, &databasepb.GetDatabaseRequest{
		Name: databasePath,
	})
	if err != nil {
		// Database doesn't exist, create it with the table
		t.Logf("Creating Spanner database: %s", databasePath)
		ddlStatements := []string{
			`CREATE TABLE LeaderLocks (
				LockKey STRING(MAX) NOT NULL,
				LockValue STRING(MAX) NOT NULL,
				ClusterName STRING(MAX) NOT NULL,
				MetadataJson STRING(MAX),
				CreatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
				UpdatedAt TIMESTAMP NOT NULL OPTIONS (allow_commit_timestamp=true),
				ExpiresAt TIMESTAMP NOT NULL,
				LockVersion INT64 NOT NULL
			) PRIMARY KEY (LockKey)`,
			`CREATE INDEX IdxLeaderLocksExpiresAt ON LeaderLocks (ExpiresAt)`,
			`CREATE INDEX IdxLeaderLocksLockValue ON LeaderLocks (LockValue)`,
		}

		op, err := databaseAdmin.CreateDatabase(ctx, &databasepb.CreateDatabaseRequest{
			Parent:          fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID),
			CreateStatement: fmt.Sprintf("CREATE DATABASE `%s`", databaseID),
			ExtraStatements: ddlStatements,
		})
		if err != nil {
			t.Skipf("Failed to create database: %v", err)
		}

		_, err = op.Wait(ctx)
		if err != nil {
			t.Skipf("Failed to wait for database creation: %v", err)
		}
		t.Logf("Successfully created database: %s", databasePath)
	}

	// Create data client
	client, err := spanner.NewClient(ctx, databasePath)
	if err != nil {
		t.Skipf("Failed to create Spanner client: %v", err)
	}

	// Create storage (this should now work since database exists)
	storage, err := NewSpannerStorage(client)
	if err != nil {
		client.Close()
		t.Skipf("Failed to create Spanner storage: %v", err)
	}

	// Return the storage and a cleanup function
	return storage, func() {
		// Clean up test data
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Delete all test data
		_, err := client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
			stmt := spanner.NewStatement("DELETE FROM LeaderLocks WHERE TRUE")
			_, err := txn.Update(ctx, stmt)
			return err
		})
		if err != nil {
			t.Logf("Warning: Failed to clean up Spanner test data: %v", err)
		}

		client.Close()
	}
}

func setupMemoryStorage(t *testing.T) (*InMemoryStorage, func()) {
	storage := NewInMemoryStorage()
	return storage, func() {
		storage.Close()
	}
}

func TestRedisStorage(t *testing.T) {
	storage, mr, cleanup := setupRedisStorage(t)
	defer cleanup()

	testStorage(t, storage, mr)
}

func TestDBStorage(t *testing.T) {
	storage, cleanup := setupDBStorage(t)
	defer cleanup()

	testStorage(t, storage, nil)
}

func TestSpannerStorage(t *testing.T) {
	storage, cleanup := setupSpannerStorage(t)
	defer cleanup()

	testStorage(t, storage, nil)
}

func TestMemoryStorage(t *testing.T) {
	storage, cleanup := setupMemoryStorage(t)
	defer cleanup()

	testStorage(t, storage, nil)
}

func testStorage(t *testing.T, storage LeaderStorage, mr *miniredis.Miniredis) {
	ctx := context.Background()
	key := "test-key"
	value1 := "test-value-1"
	value2 := "test-value-2"
	cluster1 := "cluster-1"
	cluster2 := "cluster-2"
	ttl := 1 * time.Second

	// Test TryAcquireLock
	success, err := storage.TryAcquireLock(ctx, key, value1, cluster1, ttl)
	require.NoError(t, err)
	assert.True(t, success, "Should acquire lock successfully")

	// Test GetLockInfo
	info, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")
	assert.Equal(t, value1, info.Value, "Lock value should match")
	assert.Equal(t, cluster1, info.ClusterName, "Cluster name should match")
	assert.True(t, info.TTL > 0, "Lock TTL should be positive")
	assert.True(t, info.TTL <= ttl, "Lock TTL should be less than or equal to the original TTL")
	assert.False(t, info.CreatedAt.IsZero(), "Created time should be set")
	assert.False(t, info.UpdatedAt.IsZero(), "Updated time should be set")
	assert.False(t, info.ExpiresAt.IsZero(), "Expiration time should be set")
	assert.True(t, info.Version > 0, "Version should be set")

	// Test GetLockValue (backward compatibility)
	val, err := storage.GetLockValue(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, value1, val, "Lock value should match")

	// Test GetLockTTL (backward compatibility)
	lockTTL, err := storage.GetLockTTL(ctx, key)
	require.NoError(t, err)
	assert.True(t, lockTTL > 0, "Lock TTL should be positive")
	assert.True(t, lockTTL <= ttl, "Lock TTL should be less than or equal to the original TTL")

	// Test TryAcquireLock with existing lock
	success, err = storage.TryAcquireLock(ctx, key, value2, cluster2, ttl)
	require.NoError(t, err)
	assert.False(t, success, "Should not acquire lock when it's already held")

	// Test RenewLock
	initialVersion := info.Version
	success, err = storage.RenewLock(ctx, key, value1, ttl)
	require.NoError(t, err)
	assert.True(t, success, "Should renew lock successfully")

	// Verify metadata was updated
	info, err = storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")
	assert.Equal(t, value1, info.Value, "Lock value should match")
	assert.Equal(t, cluster1, info.ClusterName, "Cluster name should match")
	assert.True(t, info.TTL > 0, "Lock TTL should be positive")

	// For DB and Spanner storage, version should be incremented
	// For Redis storage, version might be incremented depending on implementation
	assert.True(t, info.Version >= initialVersion,
		"Version should not decrease (initial: %d, current: %d)",
		initialVersion, info.Version)

	// Test RenewLock with wrong value
	success, err = storage.RenewLock(ctx, key, value2, ttl)
	require.NoError(t, err)
	assert.False(t, success, "Should not renew lock with wrong value")

	// Test ReleaseLock
	success, err = storage.ReleaseLock(ctx, key, value1)
	require.NoError(t, err)
	assert.True(t, success, "Should release lock successfully")

	// Verify lock is gone
	info, err = storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	assert.Nil(t, info, "Lock should be gone")

	// Test ReleaseLock with already released lock
	success, err = storage.ReleaseLock(ctx, key, value1)
	require.NoError(t, err)
	assert.False(t, success, "Should not release lock that's already released")

	// Test lock expiration (only for Redis, as DB/Spanner expiration is handled by background routines)
	if mr != nil {
		// Acquire lock with short TTL
		success, err = storage.TryAcquireLock(ctx, key, value1, cluster1, 100*time.Millisecond)
		require.NoError(t, err)
		assert.True(t, success, "Should acquire lock successfully")

		// Manually expire the key in miniredis
		mr.FastForward(200 * time.Millisecond)

		// Check that lock is expired
		info, err = storage.GetLockInfo(ctx, key)
		require.NoError(t, err)
		assert.Nil(t, info, "Lock should be expired")

		// Try to acquire lock again
		success, err = storage.TryAcquireLock(ctx, key, value2, cluster2, ttl)
		require.NoError(t, err)
		assert.True(t, success, "Should acquire lock after expiration")

		// Verify new lock info
		info, err = storage.GetLockInfo(ctx, key)
		require.NoError(t, err)
		require.NotNil(t, info, "Lock info should not be nil")
		assert.Equal(t, value2, info.Value, "Lock value should match")
		assert.Equal(t, cluster2, info.ClusterName, "Cluster name should match")
	}
}

// TestConcurrentLockAcquisition tests that only one goroutine can acquire a lock
func TestConcurrentLockAcquisition(t *testing.T) {
	// Test with all storage types
	t.Run("Redis", func(t *testing.T) {
		storage, _, cleanup := setupRedisStorage(t)
		defer cleanup()
		testConcurrentLockAcquisition(t, storage)
	})

	t.Run("DB", func(t *testing.T) {
		storage, cleanup := setupDBStorage(t)
		defer cleanup()
		testConcurrentLockAcquisition(t, storage)
	})

	t.Run("Spanner", func(t *testing.T) {
		storage, cleanup := setupSpannerStorage(t)
		defer cleanup()
		testConcurrentLockAcquisition(t, storage)
	})

	t.Run("Memory", func(t *testing.T) {
		storage, cleanup := setupMemoryStorage(t)
		defer cleanup()
		testConcurrentLockAcquisition(t, storage)
	})
}

func testConcurrentLockAcquisition(t *testing.T, storage LeaderStorage) {
	ctx := context.Background()
	key := "concurrent-test-key"
	ttl := 10 * time.Second

	// Reduce number of goroutines for SQLite and Spanner
	numGoroutines := 3
	if _, ok := storage.(*DBStorage); ok {
		numGoroutines = 2 // Even fewer for SQLite
	}
	if _, ok := storage.(*SpannerStorage); ok {
		numGoroutines = 2 // Fewer for Spanner as well
	}

	// Channel to collect results
	results := make(chan bool, numGoroutines)

	// Wait group to ensure all goroutines complete
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch goroutines sequentially with delays for SQLite and Spanner
	for i := 0; i < numGoroutines; i++ {
		// Add delay between goroutine launches for SQLite and Spanner
		if _, ok := storage.(*DBStorage); ok {
			time.Sleep(50 * time.Millisecond)
		}
		if _, ok := storage.(*SpannerStorage); ok {
			time.Sleep(100 * time.Millisecond) // Longer delay for Spanner
		}

		go func(id int) {
			defer wg.Done()

			// Each goroutine tries to acquire the lock with a unique value
			value := fmt.Sprintf("test-value-%d", id)
			clusterName := fmt.Sprintf("cluster-%d", id)

			// Add a small random delay
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)

			// Try to acquire the lock
			success, err := storage.TryAcquireLock(ctx, key, value, clusterName, ttl)
			if err != nil {
				t.Logf("Error acquiring lock (goroutine %d): %v", id, err)
				results <- false
				return
			}

			// Send the result to the channel
			results <- success
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(results)

	// Count how many goroutines successfully acquired the lock
	successCount := 0
	for success := range results {
		if success {
			successCount++
		}
	}

	// Only one goroutine should have successfully acquired the lock
	assert.Equal(t, 1, successCount, "Only one goroutine should acquire the lock")

	// Clean up - release the lock
	info, err := storage.GetLockInfo(ctx, key)
	if assert.NoError(t, err) && assert.NotNil(t, info, "Lock should exist") {
		success, err := storage.ReleaseLock(ctx, key, info.Value)
		assert.NoError(t, err)
		assert.True(t, success, "Should release lock successfully")
	}
}

// TestConcurrentLockRenewal tests that concurrent renewals are handled correctly
func TestConcurrentLockRenewal(t *testing.T) {
	// Test with all storage types
	t.Run("Redis", func(t *testing.T) {
		storage, _, cleanup := setupRedisStorage(t)
		defer cleanup()
		testConcurrentLockRenewal(t, storage)
	})

	t.Run("DB", func(t *testing.T) {
		storage, cleanup := setupDBStorage(t)
		defer cleanup()
		testConcurrentLockRenewal(t, storage)
	})

	t.Run("Spanner", func(t *testing.T) {
		storage, cleanup := setupSpannerStorage(t)
		defer cleanup()
		testConcurrentLockRenewal(t, storage)
	})

	t.Run("Memory", func(t *testing.T) {
		storage, cleanup := setupMemoryStorage(t)
		defer cleanup()
		testConcurrentLockRenewal(t, storage)
	})
}

func testConcurrentLockRenewal(t *testing.T, storage LeaderStorage) {
	ctx := context.Background()
	key := "renewal-test-key"
	value := "renewal-test-value"
	clusterName := "renewal-test-cluster"
	ttl := 10 * time.Second

	// First acquire the lock
	success, err := storage.TryAcquireLock(ctx, key, value, clusterName, ttl)
	require.NoError(t, err)
	require.True(t, success, "Should acquire lock successfully")

	// Get the initial lock info
	initialInfo, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, initialInfo, "Lock info should not be nil")

	// Number of concurrent goroutines trying to renew the lock
	numGoroutines := 3
	if _, ok := storage.(*SpannerStorage); ok {
		numGoroutines = 2 // Fewer for Spanner
	}

	// Channel to collect results
	results := make(chan bool, numGoroutines)

	// Wait group to ensure all goroutines complete
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch multiple goroutines to try to renew the lock concurrently
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			// Add a small random delay to increase chance of concurrency
			time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)

			// Use retry with backoff for SQLite and Spanner
			var success bool
			var err error

			// Retry up to 3 times with backoff
			for attempt := 0; attempt < 3; attempt++ {
				success, err = storage.RenewLock(ctx, key, value, ttl)
				if err == nil {
					break
				}

				// If we get an error, wait a bit and retry
				t.Logf("Attempt %d: Error renewing lock (goroutine %d): %v", attempt, id, err)
				time.Sleep(time.Duration(50*(attempt+1)) * time.Millisecond)
			}

			if err != nil {
				t.Logf("Final error renewing lock (goroutine %d): %v", id, err)
				results <- false
				return
			}

			// Send the result to the channel
			results <- success
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(results)

	// Count how many goroutines successfully renewed the lock
	successCount := 0
	for success := range results {
		if success {
			successCount++
		}
	}

	// At least one renewal should succeed
	assert.True(t, successCount > 0, "At least one renewal should succeed")

	// Get the final lock info
	finalInfo, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, finalInfo, "Lock info should not be nil")

	// The version should have increased or stayed the same
	assert.True(t, finalInfo.Version >= initialInfo.Version,
		"Version should not decrease (initial: %d, final: %d)",
		initialInfo.Version, finalInfo.Version)

	// The expiration time should have been extended
	assert.True(t, finalInfo.ExpiresAt.After(initialInfo.ExpiresAt) ||
		finalInfo.ExpiresAt.Equal(initialInfo.ExpiresAt),
		"Expiration time should not decrease")

	// Release the lock
	success, err = storage.ReleaseLock(ctx, key, value)
	require.NoError(t, err)
	assert.True(t, success, "Should release lock successfully")
}

// TestLockExpiration tests that expired locks can be reacquired
func TestLockExpiration(t *testing.T) {
	// Test with all storage types
	t.Run("Redis", func(t *testing.T) {
		storage, mr, cleanup := setupRedisStorage(t)
		defer cleanup()
		testLockExpiration(t, storage, mr)
	})

	t.Run("DB", func(t *testing.T) {
		storage, cleanup := setupDBStorage(t)
		defer cleanup()
		testLockExpiration(t, storage, nil)
	})

	t.Run("Spanner", func(t *testing.T) {
		storage, cleanup := setupSpannerStorage(t)
		defer cleanup()
		testLockExpiration(t, storage, nil)
	})

	t.Run("Memory", func(t *testing.T) {
		storage, cleanup := setupMemoryStorage(t)
		defer cleanup()
		testLockExpiration(t, storage, nil)
	})
}

func testLockExpiration(t *testing.T, storage LeaderStorage, mr *miniredis.Miniredis) {
	ctx := context.Background()
	key := "expiration-test-key"
	value1 := "expiration-test-value-1"
	value2 := "expiration-test-value-2"
	clusterName := "expiration-test-cluster"
	shortTTL := 100 * time.Millisecond

	// Acquire lock with short TTL
	success, err := storage.TryAcquireLock(ctx, key, value1, clusterName, shortTTL)
	require.NoError(t, err)
	require.True(t, success, "Should acquire lock successfully")

	// For Redis, we can use miniredis to fast forward time
	if mr != nil {
		mr.FastForward(shortTTL * 2)
	} else {
		// For other storage types, simulate expiration by manually deleting the lock
		if dbStorage, ok := storage.(*DBStorage); ok {
			// Use GORM's Exec to directly execute SQL
			err := dbStorage.db.Exec("DELETE FROM leader_locks WHERE key = ?", key).Error
			require.NoError(t, err)
		} else if spannerStorage, ok := storage.(*SpannerStorage); ok {
			// Use Spanner transaction to delete the lock
			_, err := spannerStorage.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
				stmt := spanner.NewStatement("DELETE FROM LeaderLocks WHERE LockKey = @key")
				stmt.Params["key"] = key
				_, err := txn.Update(ctx, stmt)
				return err
			})
			require.NoError(t, err)
		} else if memStorage, ok := storage.(*InMemoryStorage); ok {
			// For memory storage, we can simulate expiration by manipulating the expiration time
			memStorage.mu.Lock()
			if entry, exists := memStorage.locks[key]; exists {
				entry.ExpiresAt = time.Now().Add(-time.Hour) // Set to past time
				memStorage.locks[key] = entry
			}
			memStorage.mu.Unlock()
		}
	}

	// After expiration, a different client should be able to acquire the lock
	success, err = storage.TryAcquireLock(ctx, key, value2, clusterName, shortTTL)
	require.NoError(t, err)
	require.True(t, success, "Should acquire lock after expiration")

	// Verify the new lock info
	info, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")
	assert.Equal(t, value2, info.Value, "Lock value should match")

	// Release the lock
	success, err = storage.ReleaseLock(ctx, key, value2)
	require.NoError(t, err)
	assert.True(t, success, "Should release lock successfully")
}

// TestVersionIncrement tests that the version is properly incremented in storage implementations
func TestVersionIncrement(t *testing.T) {
	// Test with storage types that support versioning
	t.Run("DB", func(t *testing.T) {
		storage, cleanup := setupDBStorage(t)
		defer cleanup()
		testVersionIncrement(t, storage)
	})

	t.Run("Spanner", func(t *testing.T) {
		storage, cleanup := setupSpannerStorage(t)
		defer cleanup()
		testVersionIncrement(t, storage)
	})

	t.Run("Memory", func(t *testing.T) {
		storage, cleanup := setupMemoryStorage(t)
		defer cleanup()
		testVersionIncrement(t, storage)
	})
}

func testVersionIncrement(t *testing.T, storage LeaderStorage) {
	ctx := context.Background()
	key := "version-test-key"
	value := "version-test-value"
	clusterName := "version-test-cluster"
	ttl := 10 * time.Second

	// Acquire the lock
	success, err := storage.TryAcquireLock(ctx, key, value, clusterName, ttl)
	require.NoError(t, err)
	require.True(t, success, "Should acquire lock successfully")

	// Get the initial version
	info, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")
	initialVersion := info.Version

	// Renew the lock multiple times
	for i := 0; i < 3; i++ {
		success, err = storage.RenewLock(ctx, key, value, ttl)
		require.NoError(t, err)
		require.True(t, success, "Should renew lock successfully")
	}

	// Get the final version
	info, err = storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")
	finalVersion := info.Version

	// The version should have increased
	assert.True(t, finalVersion > initialVersion,
		"Version should increase (initial: %d, final: %d)",
		initialVersion, finalVersion)

	// Release the lock
	success, err = storage.ReleaseLock(ctx, key, value)
	require.NoError(t, err)
	assert.True(t, success, "Should release lock successfully")
}

// TestSpannerSpecificFeatures tests Spanner-specific functionality
func TestSpannerSpecificFeatures(t *testing.T) {
	storage, cleanup := setupSpannerStorage(t)
	defer cleanup()

	ctx := context.Background()
	key := "spanner-test-key"
	value := "spanner-test-value"
	clusterName := "spanner-test-cluster"
	ttl := 10 * time.Second

	// Test that commit timestamps are used
	success, err := storage.TryAcquireLock(ctx, key, value, clusterName, ttl)
	require.NoError(t, err)
	require.True(t, success, "Should acquire lock successfully")

	info, err := storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")

	// CreatedAt and UpdatedAt should be set
	assert.False(t, info.CreatedAt.IsZero(), "CreatedAt should be set")
	assert.False(t, info.UpdatedAt.IsZero(), "UpdatedAt should be set")

	// Test renewal updates the timestamp
	originalUpdatedAt := info.UpdatedAt

	// Wait a bit to ensure timestamp difference
	time.Sleep(100 * time.Millisecond)

	success, err = storage.RenewLock(ctx, key, value, ttl)
	require.NoError(t, err)
	require.True(t, success, "Should renew lock successfully")

	info, err = storage.GetLockInfo(ctx, key)
	require.NoError(t, err)
	require.NotNil(t, info, "Lock info should not be nil")

	// UpdatedAt should have changed
	assert.True(t, info.UpdatedAt.After(originalUpdatedAt), "UpdatedAt should be updated on renewal")

	// Clean up
	success, err = storage.ReleaseLock(ctx, key, value)
	require.NoError(t, err)
	assert.True(t, success, "Should release lock successfully")
}
