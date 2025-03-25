package leader

import (
	"context"
	"errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_model/go"
	"testing"
	"time"

	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/storage"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockLeaderStorage is a mock implementation of the LeaderStorage interface for testing
type MockLeaderStorage struct {
	mock.Mock
	failReads      bool
	failWrites     bool
	failAllOps     bool
	intermittent   bool
	failureCounter int
	failEvery      int
}

func (m *MockLeaderStorage) TryAcquireLock(ctx context.Context, key string, value string, clusterName string, ttl time.Duration) (bool, error) {
	if m.shouldFail("write") {
		return false, errors.New("simulated write failure")
	}
	args := m.Called(ctx, key, value, clusterName, ttl)
	return args.Bool(0), args.Error(1)
}

func (m *MockLeaderStorage) RenewLock(ctx context.Context, key string, value string, ttl time.Duration) (bool, error) {
	if m.shouldFail("write") {
		return false, errors.New("simulated write failure")
	}
	args := m.Called(ctx, key, value, ttl)
	return args.Bool(0), args.Error(1)
}

func (m *MockLeaderStorage) ReleaseLock(ctx context.Context, key string, value string) (bool, error) {
	if m.shouldFail("write") {
		return false, errors.New("simulated write failure")
	}
	args := m.Called(ctx, key, value)
	return args.Bool(0), args.Error(1)
}

func (m *MockLeaderStorage) GetLockInfo(ctx context.Context, key string) (*storage.LockMetadata, error) {
	if m.shouldFail("read") {
		return nil, errors.New("simulated read failure")
	}
	args := m.Called(ctx, key)
	if v := args.Get(0); v != nil {
		return v.(*storage.LockMetadata), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockLeaderStorage) GetLockValue(ctx context.Context, key string) (string, error) {
	if m.shouldFail("read") {
		return "", errors.New("simulated read failure")
	}
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockLeaderStorage) GetLockTTL(ctx context.Context, key string) (time.Duration, error) {
	if m.shouldFail("read") {
		return 0, errors.New("simulated read failure")
	}
	args := m.Called(ctx, key)
	return args.Get(0).(time.Duration), args.Error(1)
}

func (m *MockLeaderStorage) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockLeaderStorage) shouldFail(opType string) bool {
	if m.failAllOps {
		return true
	}

	if opType == "read" && m.failReads {
		if m.intermittent {
			m.failureCounter++
			if m.failureCounter%m.failEvery == 0 {
				return true
			}
			return false
		}
		return true
	}

	if opType == "write" && m.failWrites {
		if m.intermittent {
			m.failureCounter++
			if m.failureCounter%m.failEvery == 0 {
				return true
			}
			return false
		}
		return true
	}

	return false
}

// TestDatabaseReadFailure tests the behavior when the database read operations fail
func TestDatabaseReadFailure(t *testing.T) {
	// Create mocks
	mockStorage := new(MockLeaderStorage)
	mockStorage.failReads = true

	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")

	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}

	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// Set up expectations for mocks
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("", errors.New("simulated DB read failure"))

	// Attempt to acquire leadership
	ctx := context.Background()
	success := controller.TryAcquireLeadership(ctx)

	// Verify that leadership acquisition failed
	require.False(t, success, "Controller should not become leader when DB reads fail")

	// Verify that error was reported to monitoring
	healthStatus := monitoringServer.GetHealthStatus()
	require.Contains(t, healthStatus.LastError, "read")

	// Verify that metrics were updated correctly
	require.Equal(t, float64(0), metrics.IsLeader.(*testGauge).value, "IsLeader metric should be 0")
	require.Equal(t, float64(1), metrics.LeadershipFailedAttempts.(*testCounter).value, "FailedAttempts metric should be incremented")

	// Confirm the controller knows it's not the leader
	require.False(t, controller.IsLeader(), "Controller should know it's not the leader")

	// Wait a bit then verify workloads weren't started
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 0, workloadMgr.GetStartCalls(), "Workloads should not be started")

	mockStorage.AssertExpectations(t)
}

// TestDatabaseWriteFailure tests the behavior when the database write operations fail
func TestDatabaseWriteFailure(t *testing.T) {
	// Create mocks
	mockStorage := new(MockLeaderStorage)
	mockStorage.failWrites = true

	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")

	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}

	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// Set up expectations for mocks
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("", nil)
	mockStorage.On("TryAcquireLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", "", mock.Anything).
		Return(false, errors.New("simulated DB write failure"))

	// Attempt to acquire leadership
	ctx := context.Background()
	success := controller.TryAcquireLeadership(ctx)

	// Verify that leadership acquisition failed
	require.False(t, success, "Controller should not become leader when DB writes fail")

	// Verify that error was reported to monitoring
	healthStatus := monitoringServer.GetHealthStatus()
	require.Contains(t, healthStatus.LastError, "acquire leadership")

	// Verify that metrics were updated correctly
	require.Equal(t, float64(0), metrics.IsLeader.(*testGauge).value, "IsLeader metric should be 0")
	require.Equal(t, float64(1), metrics.LeadershipFailedAttempts.(*testCounter).value, "FailedAttempts metric should be incremented")

	// Confirm the controller knows it's not the leader
	require.False(t, controller.IsLeader(), "Controller should know it's not the leader")

	// Wait a bit then verify workloads weren't started
	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 0, workloadMgr.GetStartCalls(), "Workloads should not be started")

	mockStorage.AssertExpectations(t)
}

// TestLeaderRenewalFailure tests the behavior when the leader fails to renew its lock
func TestLeaderRenewalFailure(t *testing.T) {
	// Create mocks
	mockStorage := new(MockLeaderStorage)
	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")
	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}

	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// Setup for successful acquisition of leadership
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("", nil).Once()
	mockStorage.On("TryAcquireLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", "", mock.Anything).
		Return(true, nil).Once()

	// Then fail on renewal
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("test-controller", nil).Once()
	mockStorage.On("RenewLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", mock.Anything).
		Return(false, errors.New("simulated DB renewal failure")).Once()

	// Attempt to acquire and then renew leadership
	ctx := context.Background()
	success := controller.TryAcquireLeadership(ctx)
	require.True(t, success, "Initial leadership acquisition should succeed")
	require.True(t, controller.IsLeader(), "Controller should think it's the leader")

	// Simulate workload start (normally this happens in a goroutine after acquisition)
	require.NoError(t, controller.startWorkloads())
	require.Equal(t, 1, workloadMgr.GetStartCalls(), "Workloads should be started after leadership acquisition")

	// Now try to renew and fail
	success = controller.RenewLeadership(ctx)
	require.False(t, success, "Leadership renewal should fail")

	// Verify the controller has given up leadership and stopped workloads
	require.False(t, controller.IsLeader(), "Controller should not think it's the leader anymore")
	require.Equal(t, 1, workloadMgr.GetStopCalls(), "Workloads should be stopped after leadership loss")

	mockStorage.AssertExpectations(t)
}

// TestSplitBrainDetection tests the behavior when a controller thinks it's the leader
// but the DB indicates another controller is the actual leader
func TestSplitBrainDetection(t *testing.T) {
	// Create mocks
	mockStorage := new(MockLeaderStorage)
	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")
	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}

	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// First acquisition is successful
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("", nil).Once()
	mockStorage.On("TryAcquireLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", "", mock.Anything).
		Return(true, nil).Once()

	// Set up expectations for split-brain scenario
	// Later checking reveals someone else has the lock now
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("another-controller", nil).Once()

	// Acquire leadership
	ctx := context.Background()
	success := controller.TryAcquireLeadership(ctx)
	require.True(t, success, "Initial leadership acquisition should succeed")

	// Manually set controller as leader
	controller.leaderMutex.Lock()
	controller.isLeader = true
	controller.leaderMutex.Unlock()

	// Start monitoring the lock
	controller.monitorLock(ctx)

	// Now run one cycle of lock monitoring
	mockStorage.On("GetLockInfo", mock.Anything, "highlander-leader-lock:test-tenant").
		Return(&storage.LockMetadata{
			Value: "another-controller",
			TTL:   30 * time.Second,
		}, nil).Once()

	// Let's manually trigger the monitor function
	go func() {
		time.Sleep(100 * time.Millisecond)
		controller.monitorLock(ctx)
	}()

	// Wait a bit for the monitoring goroutine to run
	time.Sleep(200 * time.Millisecond)

	// Verify that the controller detected the split-brain and gave up leadership
	require.False(t, controller.IsLeader(), "Controller should not think it's the leader after split-brain detection")
	require.Equal(t, 1, workloadMgr.GetStopCalls(), "Workloads should be stopped after split-brain detection")

	// Check that monitoring was updated correctly
	healthStatus := monitoringServer.GetHealthStatus()
	require.Equal(t, "another-controller", healthStatus.CurrentLeader, "Current leader should be updated")

	mockStorage.AssertExpectations(t)
}

// TestIntermittentDatabaseFailure tests graceful handling of temporary DB failures
func TestIntermittentDatabaseFailure(t *testing.T) {
	mockStorage := new(MockLeaderStorage)
	mockStorage.intermittent = true
	mockStorage.failReads = true
	mockStorage.failEvery = 2 // Fail every other read

	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")
	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}
	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// Set up expectations for intermittent failures
	// First read succeeds
	mockStorage.On("GetLockValue", mock.Anything, "highlander-leader-lock:test-tenant").
		Return("", nil).Times(3)

	// Acquisition succeeds
	mockStorage.On("TryAcquireLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", "", mock.Anything).
		Return(true, nil).Once()

	// Renewal calls - some will fail intermittently but handled gracefully
	mockStorage.On("RenewLock", mock.Anything, "highlander-leader-lock:test-tenant",
		"test-controller", mock.Anything).
		Return(true, nil).Times(2)

	// Make controller the leader
	ctx := context.Background()
	success := controller.TryAcquireLeadership(ctx)
	require.True(t, success, "Initial leadership acquisition should succeed")

	// Test first renewal (should succeed despite intermittent failures)
	success = controller.RenewLeadership(ctx)
	require.True(t, success, "First renewal should succeed")

	// Test another renewal
	success = controller.RenewLeadership(ctx)
	require.True(t, success, "Second renewal should succeed")

	// Verify controller still thinks it's leader
	require.True(t, controller.IsLeader(), "Controller should still be leader after intermittent DB failures")

	mockStorage.AssertExpectations(t)
}

// TestProlongedDatabaseFailure tests transition to a SplitBrain/BadState when
// database connectivity is lost for too long (> configured threshold)
func TestProlongedDatabaseFailure(t *testing.T) {
	// Create mocks with complete failure
	mockStorage := new(MockLeaderStorage)
	mockStorage.failAllOps = true

	metrics := monitoring.NewControllerMetrics(nil)
	monitoringServer := monitoring.NewMonitoringServer(metrics, "test", "test")
	workloadMgr := NewMockWorkloadManager()

	// Create a cluster config
	cluster := common.ClusterConfig{
		Name: "test-cluster",
	}

	// Create the controller with the mock storage
	controller := NewLeaderController(
		"test-controller",
		"test-tenant",
		&dbFailure,
		&cluster,
		mockStorage,
		metrics,
		monitoringServer,
		workloadMgr,
	)

	// Set a shorter threshold for testing (normally this would be 5 minutes)
	controller.dbFailureThreshold = 500 * time.Millisecond

	// Set up expectations - all operations will fail due to failAllOps
	mockStorage.On("GetLockValue", mock.Anything, mock.Anything).
		Return("", errors.New("DB connection lost"))
	mockStorage.On("GetLockInfo", mock.Anything, mock.Anything).
		Return(nil, errors.New("DB connection lost"))

	// Manually make controller think it's the leader
	controller.leaderMutex.Lock()
	controller.isLeader = true
	controller.lastDBFailureTime = time.Now().Add(-time.Second) // Set failure started 1 second ago
	controller.leaderMutex.Unlock()

	// Start the DB health check function in a goroutine
	go controller.monitorDBHealth(context.Background())

	// Wait for the threshold to be crossed
	time.Sleep(600 * time.Millisecond)

	// Verify controller has entered bad state and stopped workloads
	require.False(t, controller.IsLeader(), "Controller should not be leader after prolonged DB failure")
	require.Equal(t, 1, workloadMgr.GetStopCalls(), "Workloads should be stopped after prolonged DB failure")

	// Check monitoring was updated
	healthStatus := monitoringServer.GetHealthStatus()
	require.Contains(t, healthStatus.LastError, "prolonged database failure")
	require.False(t, healthStatus.DBConnected, "DB should be reported as disconnected")
	require.Equal(t, "SPLIT_BRAIN_RISK", healthStatus.ControllerState, "Controller should be in SPLIT_BRAIN_RISK state")

	mockStorage.AssertExpectations(t)
}

// Helper types for mocking metrics
type testGauge struct {
	value float64
}

func (g *testGauge) Desc() *prometheus.Desc {
	return nil
}

func (g *testGauge) Write(*io_prometheus_client.Metric) error {
	return nil
}

func (g *testGauge) Describe(chan<- *prometheus.Desc) {
}

func (g *testGauge) Collect(chan<- prometheus.Metric) {
}

var _ prometheus.Gauge = &testGauge{}

func (g *testGauge) Set(value float64) {
	g.value = value
}

func (g *testGauge) Inc() {
	g.value++
}

func (g *testGauge) Dec() {
	g.value--
}

func (g *testGauge) Add(value float64) {
	g.value += value
}

func (g *testGauge) Sub(value float64) {
	g.value -= value
}

func (g *testGauge) SetToCurrentTime() {
	g.value = float64(time.Now().Unix())
}

type testCounter struct {
	value float64
}

func (c *testCounter) Desc() *prometheus.Desc {
	return nil
}

func (c *testCounter) Write(*io_prometheus_client.Metric) error {
	return nil
}

func (c *testCounter) Describe(chan<- *prometheus.Desc) {
}

func (c *testCounter) Collect(chan<- prometheus.Metric) {
}

var _ prometheus.Counter = &testCounter{}

func (c *testCounter) Inc() {
	c.value++
}

func (c *testCounter) Add(value float64) {
	c.value += value
}
