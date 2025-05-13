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

// Package leader implements distributed leader election and failover management for k8-highlander.
//
// This package provides the core leader election mechanism that ensures only one controller
// instance is active and managing workloads at any given time. It uses a distributed lock
// (implemented with Redis or database storage) to elect a leader, and handles automatic
// failover when the leader becomes unhealthy or loses connectivity.
//
// Key features:
// - Distributed lock-based leader election with TTL to prevent split-brain scenarios
// - Automatic detection and handling of leader failures
// - Graceful workload handover during failover
// - Self-restart detection to minimize unnecessary workload recreation
// - Continuous monitoring of leader health, cluster health, and workload status
// - Controlled shutdown for clean leader transitions
//
// The leader election process works as follows:
// 1. Each controller instance attempts to acquire a lock with a TTL
// 2. The instance that successfully acquires the lock becomes the leader
// 3. The leader periodically renews its lock to maintain leadership
// 4. If the leader fails to renew the lock (due to crash, network issues, etc.),
//    another instance can acquire the lock and take over
// 5. During failover, the new leader waits for workloads from the previous leader
//    to terminate before starting its own workloads
//
// This implementation is designed to be resilient against network partitions,
// instance failures, and other distributed systems challenges while preventing
// split-brain scenarios where multiple leaders could be active simultaneously.

package leader

import (
	"context"
	ierrors "errors"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/bhatti/k8-highlander/pkg/storage"
	"github.com/bhatti/k8-highlander/pkg/workloads"
	"github.com/go-redis/redis/v8"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	leaderLockKeyFormat = "highlander-leader-lock:%s" // Format: highlander-leader-lock:<tenant>
	defaultTenant       = "default"
	LockTTL             = 60 * time.Second
	RenewInterval       = 5 * time.Second

	// Constants for DB failure tolerance
	maxConsecutiveDBFailures = 3

	// Constants for workload management
	workloadOwnerAnnotation    = "k8-highlander/owner"
	workloadShutdownAnnotation = "k8-highlander/controlled-shutdown"
	maxWorkloadWaitTime        = 60 * time.Second
	workloadCheckInterval      = 2 * time.Second
)

// LeaderController manages the leader election process and workload lifecycle.
// It handles lock acquisition, renewal, monitoring, and graceful transitions
// between leaders.
type LeaderController struct {
	storage          storage.LeaderStorage
	id               string
	cluster          *common.ClusterConfig
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
	workloadManager  workloads.Manager
	crdWatcher       *workloads.CRDWatcher
	leaderLockKey    string
	remainFollower   bool

	isLeader        bool
	controllerState common.ControllerState
	dbFailureCount  int
	stateMutex      sync.RWMutex
	workloadMutex   sync.RWMutex
	workloadCtx     context.Context
	workloadCancel  context.CancelFunc
	stopCh          chan struct{}

	// Track previous leader for failover
	previousLeader string

	// Track our own previous instance for restart detection
	previousSelfID string

	// Track when we last released leadership
	lastLeadershipReleaseTime time.Time

	// Flag to indicate if this is a self restart
	isSelfRestart bool
}

// NewLeaderController creates a new leader controller
func NewLeaderController(
	storage storage.LeaderStorage,
	id string,
	activeCluster *common.ClusterConfig,
	metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer,
	workloadManager workloads.Manager,
	tenant string,
	remainFollower bool,
) *LeaderController {
	// Use default tenant if empty
	if tenant == "" {
		tenant = defaultTenant
	}

	monitoringServer.UpdateControllerState(common.StateNormal)

	// Create the CRD watcher
	crdWatcher := workloads.NewCRDWatcher(
		activeCluster.GetDynamicClient(),
		activeCluster.GetClient(),
		workloadManager,
		metrics,
		monitoringServer,
	)

	return &LeaderController{
		storage:          storage,
		id:               id,
		cluster:          activeCluster,
		metrics:          metrics,
		monitoringServer: monitoringServer,
		workloadManager:  workloadManager,
		crdWatcher:       crdWatcher,
		leaderLockKey:    fmt.Sprintf(leaderLockKeyFormat, tenant),
		stopCh:           make(chan struct{}),
		controllerState:  common.StateNormal,
		dbFailureCount:   0,
		remainFollower:   remainFollower,
	}
}

// Start begins the leader election process, setting up monitoring and attempting
// to acquire leadership immediately.
func (lc *LeaderController) Start(ctx context.Context) error {
	ctx = common.StoreStartTime(ctx)
	started := time.Now()
	// Start monitoring cluster health
	go lc.monitorClusterHealth(ctx)

	// Start lock monitoring
	go lc.monitorLock(ctx)

	// Start workload monitoring
	go lc.monitorWorkloads(ctx)

	// Start the main leader election loop to ensure lease renewal
	go lc.runLeaderElectionLoop(ctx)

	// Try to acquire leadership immediately
	if lc.TryAcquireLeadership(ctx) {
		lc.SetLeader(true)

		// Create a workload context
		lc.createWorkloadContext(ctx)

		// Start a goroutine to handle workload startup
		// This ensures we don't block lease renewal while waiting for pods
		go func() {
			// Wait for any existing workloads to terminate before starting new ones
			if err := lc.waitForPreviousWorkloads(ctx); err != nil {
				klog.Warningf("Start error waiting for previous workloads: %v [elapsed %s]", err, time.Since(started))
			}

			// Start workloads
			if err := lc.startWorkloads(); err != nil {
				if ierrors.Is(err, &common.SevereError{}) {
					klog.Errorf("Encountered severe error starting workloads, stopping controller: %v", err)
					// Release leadership since we can't properly function
					lc.releaseLeadership(ctx)
					lc.SetLeader(false)
					return
				}

				klog.Warningf("Encountered non-severe errors starting workloads (%s): %v",
					reflect.TypeOf(err).String(), err)
				lc.monitoringServer.SetError(fmt.Errorf("non-severe errors starting workloads: %w", err))
			}
		}()
	}

	return nil
}

// Stop gracefully shuts down the leader controller, releasing leadership if held
// and stopping managed workloads.
func (lc *LeaderController) Stop(ctx context.Context) error {
	klog.Infof("Stopping leader controller with ID %s [elapsed %s] (leader %v)",
		lc.id, common.GetElapsedTime(ctx), lc.monitoringServer.GetLeaderInfo())

	// Use a mutex to ensure thread safety
	lc.workloadMutex.Lock()
	defer lc.workloadMutex.Unlock()

	// Check if already stopped
	select {
	case <-lc.stopCh:
		// Channel is already closed, nothing to do
		return nil
	default:
		// Channel is still open, close it
		close(lc.stopCh)
	}

	// Stop the CRD watcher regardless of leader status
	if lc.crdWatcher != nil {
		klog.Infof("Stopping CRD watcher %s...", lc.id)
		if err := lc.crdWatcher.Stop(ctx); err != nil {
			klog.Errorf("Failed to stop CRD watcher: %v", err)
		}
	}

	// If we're the leader, release leadership and stop workloads
	if lc.isLeader {
		// Create a context with a shorter timeout for releasing leadership
		releaseCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Release leadership
		klog.Infof("Releasing leadership %s...", lc.id)
		lc.releaseLeadership(releaseCtx)
		lc.SetLeader(false)

		// Stop workloads with the original context
		klog.Infof("Stopping all workloads %s...", lc.id)
		if err := lc.workloadManager.StopAll(ctx); err != nil {
			klog.Errorf("Failed to stop workloads %s: %v", lc.id, err)
			lc.monitoringServer.SetError(fmt.Errorf("failed to stop workloads %s: %w", lc.id, err))
		}

		// Clean up the workload manager
		if cleanup, ok := lc.workloadManager.(interface{ Cleanup() }); ok {
			cleanup.Cleanup()
		}
	}
	klog.Infof("Leader controller with ID %s stopped", lc.id)

	return nil
}

func (lc *LeaderController) resetDBFailures() {
	lc.stateMutex.Lock()
	lc.dbFailureCount = 0
	lc.stateMutex.Unlock()
}

func (lc *LeaderController) incrDBFailures() {
	lc.stateMutex.Lock()
	lc.dbFailureCount++
	lc.stateMutex.Unlock()
}

func (lc *LeaderController) getDBFailures() int {
	lc.stateMutex.RLock()
	defer lc.stateMutex.RUnlock()
	return lc.dbFailureCount
}

func (lc *LeaderController) GetControllerState() common.ControllerState {
	lc.stateMutex.RLock()
	defer lc.stateMutex.RUnlock()
	return lc.controllerState
}

func (lc *LeaderController) SetControllerState(state common.ControllerState) {
	lc.stateMutex.Lock()
	defer lc.stateMutex.Unlock()

	// Only log if state is changing
	if lc.controllerState != state {
		klog.V(4).Infof("Controller state changing from %s to %s", lc.controllerState, state)
		lc.controllerState = state

		// Update monitoring status
		lc.monitoringServer.UpdateControllerState(state)
	}
}

// TryAcquireLeadership attempts to acquire leadership by obtaining a distributed lock.
// It handles various edge cases like self-restart detection.
func (lc *LeaderController) TryAcquireLeadership(ctx context.Context) bool {
	// Log the lock state before acquire
	//lockInfo, err := lc.storage.GetLockInfo(ctx, lc.leaderLockKey)
	//if err != nil {
	//	klog.Errorf("Error getting lock info before acquire: %v", err)
	//} else if lockInfo == nil {
	//	klog.Warningf("Lock info is nil before acquire - key may already be gone")
	//} else {
	//	klog.Infof("Lock info before acquire: value=%s, ttl=%v, version=%d",
	//		lockInfo.Value, lockInfo.TTL, lockInfo.Version)
	//}
	if lc.remainFollower {
		return false
	}
	startTime := time.Now()

	// Get the current leader (if any) before we try to acquire
	currentLeader, err := lc.getCurrentLeader(ctx, 0)
	if err != nil {
		klog.Warningf("Error getting current leader: %v  (failure count: %d)", err, lc.getDBFailures())

		if isConnectionError(err) {
			lc.incrDBFailures()

			if lc.GetControllerState() != common.StateDegraded {
				klog.Warningf("Setting controller state to Degraded (failure count: %d)", lc.getDBFailures())
				lc.SetControllerState(common.StateDegraded)
				lc.monitoringServer.UpdateControllerState(common.StateDegraded)
			}
		}

		// If we're not already leader or exceeded failure threshold, return false
		lc.metrics.LeadershipFailedAttempts.Inc()
		lc.monitoringServer.SetError(fmt.Errorf("failed to acquire leadership: %w", err))
		// Continue anyway, but log the error
	}

	// IMPORTANT: Always store the current leader as previous leader if it exists
	// This ensures we always know who was the leader before us
	if currentLeader != "" && currentLeader != lc.id {
		//klog.Infof("Current leader is %s, storing as previous leader", currentLeader)
		lc.previousLeader = currentLeader
	}

	// Check if this is a restart of the same leader
	isSelfRestart := false
	if currentLeader == "" && lc.previousSelfID == lc.id {
		// This is likely a restart of the same leader
		timeSinceRelease := time.Since(lc.lastLeadershipReleaseTime)
		if timeSinceRelease < 5*time.Minute { // Consider it a restart if within 5 minutes
			isSelfRestart = true
			klog.Infof("Detected self restart (previous instance of %s)", lc.id)
		}
	}

	if isSelfRestart {
		// If this is a self restart, we are our own previous leader
		lc.previousLeader = lc.id

		// Set a flag in the controller to indicate this is a self restart
		// This will be used in waitForPreviousWorkloads
		lc.isSelfRestart = true

		klog.Infof("Self restart: setting previous leader to self (%s)", lc.id)
	}

	// Try to set the key with NX (only if it doesn't exist) and expiration
	success, err := lc.storage.TryAcquireLock(ctx, lc.leaderLockKey, lc.id, currentLeader, LockTTL)
	duration := time.Since(startTime)

	// Record Redis operation
	lc.metrics.RecordDBOperation("setNX", duration, err)

	if err != nil {
		klog.Errorf("Error trying to acquire leadership: %v (failure count %d)", err, lc.getDBFailures())
		lc.metrics.LeadershipFailedAttempts.Inc()
		lc.monitoringServer.SetError(fmt.Errorf("failed to acquire leadership: %w (failure count %d)", err, lc.getDBFailures()))
		return false
	}

	// Reset DB failure count on successful DB operation
	{
		if lc.getDBFailures() > 0 {
			klog.Infof("DB connection restored, resetting failure count (was %d)", lc.getDBFailures())
			lc.resetDBFailures()
		}

		if lc.GetControllerState() == common.StateDegraded {
			klog.Infof("DB connection restored, setting controller state back to Normal")
			lc.SetControllerState(common.StateNormal)
			lc.monitoringServer.UpdateControllerState(common.StateNormal)
		}
	}

	// Only update status if we successfully acquired the lock
	if success {
		klog.Infof("Successfully acquired leadership with value %s", lc.id)

		// If there was a previous leader and it wasn't us, record the transition
		if currentLeader != "" && currentLeader != lc.id && !isSelfRestart {
			lc.monitoringServer.UpdateLastLeader(currentLeader)
			lc.monitoringServer.RecordFailover(currentLeader, lc.id, 0, nil)
		} else if isSelfRestart {
			klog.Infof("Self restart: reacquired leadership after restart")
			// We might want to record this differently in metrics/monitoring
			lc.monitoringServer.UpdateLastLeader(lc.id)
		}

		// Update monitoring
		lc.monitoringServer.UpdateLeaderStatus(true, lc.id, lc.cluster.Name)
		lc.monitoringServer.UpdateCurrentLeader(lc.id)
		lc.monitoringServer.ClearError()

		// Update metrics
		lc.metrics.IsLeader.Set(1)
		lc.metrics.LeadershipAcquisitions.Inc()

		// Store our ID for restart detection
		lc.previousSelfID = lc.id

		verifyLeader, verifyErr := lc.getCurrentLeader(ctx, 0)
		if verifyErr != nil {
			klog.Errorf("Error verifying leadership acquisition: %v [elapsed %s]", verifyErr, time.Since(startTime))
		} else if verifyLeader != lc.id {
			klog.Warningf("Leadership verification failed! Expected %s, got %s [elapsed %s]", lc.id, verifyLeader, time.Since(startTime))
		} else {
			klog.Infof("Leadership acquisition verified successfully [elapsed %s]", time.Since(startTime))
		}

		return true
	} else {
		klog.V(4).Infof("Failed to acquire leadership, lock already held [elapsed %s]", time.Since(startTime))

		// Update monitoring
		lc.monitoringServer.UpdateLeaderStatus(false, lc.id, lc.cluster.Name)

		// If we know who has the lock, update current leader
		if currentLeader != "" {
			lc.monitoringServer.UpdateCurrentLeader(currentLeader)
		}

		// Update metrics
		lc.metrics.IsLeader.Set(0)

		return false
	}
}

// RenewLeadership attempts to renew leadership
func (lc *LeaderController) RenewLeadership(ctx context.Context) bool {
	startTime := time.Now()

	// Check if we still own the lock
	val, err := lc.storage.GetLockValue(ctx, lc.leaderLockKey)
	duration := time.Since(startTime)

	// Record Redis operation
	lc.metrics.RecordDBOperation("get", duration, err)

	// Handle DB errors with more resilience
	if err != nil {
		klog.Warningf("Error checking leadership: %v", err)
		lc.monitoringServer.SetError(fmt.Errorf("error checking leadership: %w", err))

		if isConnectionError(err) {
			// Increment failure count
			lc.incrDBFailures()

			// Set to degraded state when we have DB failures
			if lc.GetControllerState() != common.StateDegraded {
				klog.Warningf("Setting controller state to Degraded after DB error (failure count: %d)",
					lc.getDBFailures())
				lc.SetControllerState(common.StateDegraded)
				lc.monitoringServer.UpdateControllerState(common.StateDegraded)
			}

			// Only give up leadership after consecutive failures exceed threshold
			if lc.getDBFailures() >= maxConsecutiveDBFailures {
				klog.Errorf("DB failure count (%d) exceeded maximum (%d), relinquishing leadership",
					lc.getDBFailures(), maxConsecutiveDBFailures)
				return false
			}

			klog.Warningf("Temporary DB error, maintaining leadership (failure count: %d)", lc.getDBFailures())

			// We still return true to maintain leadership during temporary DB issues
			return true
		}
		// For non-connection errors, handle normally (don't maintain leadership)
		return false
	}

	// DB operation succeeded, reset failure count and state if needed
	{
		if lc.getDBFailures() > 0 {
			klog.Infof("DB connection restored, resetting failure count (was %d)", lc.getDBFailures())
			lc.resetDBFailures()
		}

		if lc.GetControllerState() == common.StateDegraded {
			klog.Infof("DB connection restored, setting controller state back to Normal")
			lc.SetControllerState(common.StateNormal)
			lc.monitoringServer.UpdateControllerState(common.StateNormal)
		}

	}
	if val == "" {
		// Key doesn't exist, try to reacquire
		klog.Warning("Leadership key doesn't exist, trying to reacquire")
		return lc.TryAcquireLeadership(ctx)
	}

	if val != lc.id {
		// Someone else is the leader
		klog.Warningf("Leadership taken by another instance: %s", val)

		// Update current leader tracking
		lc.monitoringServer.UpdateCurrentLeader(val)

		// Record the leadership transition
		lc.monitoringServer.RecordFailover(lc.id, val, 0,
			fmt.Errorf("leadership taken by another instance during renewal"))

		return false
	}

	// We're still the leader, extend the TTL
	startTime = time.Now()
	success, err := lc.storage.RenewLock(ctx, lc.leaderLockKey, lc.id, LockTTL)
	duration = time.Since(startTime)

	// Record Redis operation
	lc.metrics.RecordDBOperation("expire", duration, err)

	if err != nil {
		klog.Warningf("Error renewing leadership: %v", err)
		lc.monitoringServer.SetError(fmt.Errorf("error renewing leadership: %w", err))
		if isConnectionError(err) {
			// Increment failure count
			lc.incrDBFailures()

			// Set to degraded state
			if lc.GetControllerState() != common.StateDegraded {
				klog.Warningf("Setting controller state to Degraded (failure count: %d)", lc.getDBFailures())
				lc.SetControllerState(common.StateDegraded)
				lc.monitoringServer.UpdateControllerState(common.StateDegraded)
			}

			// Only give up leadership after consecutive failures exceed threshold
			if lc.getDBFailures() >= maxConsecutiveDBFailures {
				klog.Errorf("DB failure count (%d) exceeded maximum (%d), relinquishing leadership",
					lc.getDBFailures(), maxConsecutiveDBFailures)
				return false
			}

			return true // Maintain leadership during temporary failures
		}

		return false
	}

	// DB operation succeeded, reset failure count and state if needed
	{
		if lc.getDBFailures() > 0 {
			klog.Infof("DB connection restored, resetting failure count (was %d)", lc.getDBFailures())
			lc.resetDBFailures()
		}

		if lc.GetControllerState() == common.StateDegraded {
			klog.Infof("DB connection restored, setting controller state back to Normal")
			lc.SetControllerState(common.StateNormal)
			lc.monitoringServer.UpdateControllerState(common.StateNormal)
		}
	}

	// Get TTL for metrics
	if ttl, err := lc.storage.GetLockTTL(ctx, lc.leaderLockKey); err == nil {
		// Update TTL metric
		lc.metrics.DBLockTTL.Set(ttl.Seconds())
	}

	return success
}

// releaseLeadership releases leadership
func (lc *LeaderController) releaseLeadership(ctx context.Context) {
	// Log the lock state before release
	lockInfo, err := lc.storage.GetLockInfo(ctx, lc.leaderLockKey)
	if err != nil {
		klog.Errorf("Error getting lock info before release: %v", err)
	} else if lockInfo == nil {
		klog.Warningf("Lock info is nil before release - key may already be gone")
	} else {
		klog.Infof("Lock info before release: value=%s, ttl=%v, version=%d",
			lockInfo.Value, lockInfo.TTL, lockInfo.Version)
	}

	startTime := time.Now()
	success, err := lc.storage.ReleaseLock(ctx, lc.leaderLockKey, lc.id)
	duration := time.Since(startTime)

	// Record Redis operation
	lc.metrics.RecordDBOperation("eval", duration, err)

	if err != nil {
		klog.Errorf("Error releasing leadership: %v", err)
		lc.monitoringServer.SetError(fmt.Errorf("error releasing leadership: %w", err))
		return
	}

	if success {
		klog.Infof("Leadership released successfully")

		// Verify the key is actually gone
		verifyVal, verifyErr := lc.storage.GetLockValue(ctx, lc.leaderLockKey)
		if verifyErr != nil {
			klog.Errorf("Error verifying lock release: %v", verifyErr)
		} else if verifyVal != "" {
			klog.Warningf("Lock still exists after release with value: %s", verifyVal)
		} else {
			klog.Infof("Verified lock is gone after release")
		}

		// Record when we released leadership for restart detection
		lc.lastLeadershipReleaseTime = time.Now()
	} else {
		klog.Warningf("Failed to release leadership, lock was modified or already released")
	}

	// Update monitoring
	lc.monitoringServer.UpdateLeaderStatus(false, lc.id, lc.cluster.Name)

	// Update metrics
	lc.metrics.IsLeader.Set(0)
	lc.metrics.LeadershipLosses.Inc()
}

// GetMetrics returns the metrics for this controller
func (lc *LeaderController) GetMetrics() *monitoring.ControllerMetrics {
	return lc.metrics
}

// IsLeader returns whether this controller is currently the leader
func (lc *LeaderController) IsLeader() bool {
	lc.stateMutex.RLock()
	defer lc.stateMutex.RUnlock()
	return lc.isLeader
}

func (lc *LeaderController) SetLeader(leader bool) {
	lc.stateMutex.Lock()
	lc.isLeader = leader
	lc.stateMutex.Unlock()
}

// TriggerClusterHealthCheck manually triggers a cluster health check (for testing)
func (lc *LeaderController) TriggerClusterHealthCheck(ctx context.Context) {
	// Check if our cluster is healthy
	isHealthy := lc.IsClusterHealthy(ctx, *lc.cluster)

	// Update monitoring
	lc.monitoringServer.UpdateClusterHealth(lc.cluster.Name, isHealthy)

	// If cluster is not healthy, and we're the leader, we should release leadership
	if !isHealthy && lc.IsLeader() {
		lc.workloadMutex.Lock()
		defer lc.workloadMutex.Unlock()

		klog.Warning("Cluster is unhealthy, releasing leadership")

		// Record failover start time
		startTime := time.Now()

		// Release leadership
		lc.releaseLeadership(ctx)
		lc.SetLeader(false)

		// Stop workloads
		_ = lc.stopWorkloads()

		// Record failover metrics
		duration := time.Since(startTime)
		lc.monitoringServer.RecordFailover(lc.id, "", duration,
			fmt.Errorf("released leadership due to unhealthy cluster %s", lc.cluster.Name))
	}
}

// monitorClusterHealth monitors the health of clusters
func (lc *LeaderController) monitorClusterHealth(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-lc.stopCh:
			return
		case <-ticker.C:
			// Check if our cluster is healthy
			isHealthy := lc.IsClusterHealthy(ctx, *lc.cluster)

			// Update monitoring
			lc.monitoringServer.UpdateClusterHealth(lc.cluster.Name, isHealthy)

			// If cluster is not healthy, and we're the leader, we should release leadership
			if !isHealthy && lc.IsLeader() {
				lc.workloadMutex.Lock()
				klog.Warning("Cluster is unhealthy, releasing leadership")

				startTime := time.Now()

				// Release leadership
				lc.releaseLeadership(ctx)
				lc.SetLeader(false)

				// Stop workloads
				_ = lc.stopWorkloads()
				duration := time.Since(startTime)

				// We don't know the new leader yet, but we record that we released leadership
				lc.monitoringServer.RecordFailover(lc.id, "", duration,
					fmt.Errorf("released leadership due to unhealthy cluster"))
				lc.workloadMutex.Unlock()
			}
		}
	}
}

// IsClusterHealthy checks if a cluster is healthy
func (lc *LeaderController) IsClusterHealthy(ctx context.Context, cluster common.ClusterConfig) bool {
	if cluster.GetClient() == nil {
		return false
	}

	// Create a context with timeout
	healthCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try to list nodes
	_, err := cluster.GetClient().CoreV1().Nodes().List(healthCtx, metav1.ListOptions{Limit: 1})
	if err != nil {
		klog.Warningf("Cluster %s health check failed: %v", cluster.Name, err)
		return false
	}

	return true
}

// monitorLock monitors the lock status
func (lc *LeaderController) monitorLock(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-lc.stopCh:
			return
		case <-ticker.C:
			// Check current lock state
			startTime := time.Now()
			val, err := lc.storage.GetLockValue(ctx, lc.leaderLockKey)
			duration := time.Since(startTime)

			// Record Redis operation (but don't count this as an error)
			if err == nil {
				lc.metrics.RecordDBOperation("get", duration, nil)
			}

			if err != nil {
				klog.Errorf("Lock monitoring: Error checking lock: %v (failure count %d)", err, lc.getDBFailures())

				// Update Redis status
				lc.monitoringServer.UpdateDBStatus(false, 0, lc.getDBFailures())
			} else if val == "" {
				klog.V(4).Info("Lock monitoring: No lock currently held")

				// Update Redis status
				lc.monitoringServer.UpdateDBStatus(true, 0, lc.getDBFailures())

				// Clear current leader
				lc.monitoringServer.UpdateCurrentLeader("")
			} else {
				// Get TTL
				ttl, _ := lc.storage.GetLockTTL(ctx, lc.leaderLockKey)

				klog.V(4).Infof("Lock monitoring: Lock held by %s, TTL: %s, our id: %s",
					val, ttl.String(), lc.id)

				// Update Redis status
				lc.monitoringServer.UpdateDBStatus(true, ttl.Seconds(), lc.getDBFailures())

				// Update TTL metric
				lc.metrics.DBLockTTL.Set(ttl.Seconds())

				// Update current leader tracking
				lc.monitoringServer.UpdateCurrentLeader(val)

				// If we previously thought we were the leader but the key shows someone else...
				lc.workloadMutex.Lock()
				if lc.IsLeader() && val != lc.id {
					klog.Warningf("Leadership taken by another instance: %s", val)

					// Update our state
					lc.SetLeader(false)

					// Stop workloads
					_ = lc.stopWorkloads()

					// Record the failover
					lc.monitoringServer.RecordFailover(lc.id, val, 0,
						fmt.Errorf("leadership taken by another instance"))

					// Update monitoring
					lc.monitoringServer.UpdateLeaderStatus(false, lc.id, lc.cluster.Name)
				}
				lc.workloadMutex.Unlock()
			}
		}
	}
}

// runLeaderElectionLoop runs the main leader election loop
func (lc *LeaderController) runLeaderElectionLoop(ctx context.Context) {
	ticker := time.NewTicker(RenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			klog.Infof("Context canceled/done, stopping leader election loop [elapsed: %s]",
				common.GetElapsedTime(ctx))
			return
		case <-lc.stopCh:
			klog.Infof("Leader controller stopped, stopping leader election loop [elapsed: %s]",
				common.GetElapsedTime(ctx))
			return
		case <-ticker.C:
			lc.workloadMutex.Lock()

			if lc.IsLeader() {
				// If we're the leader, try to renew
				if !lc.RenewLeadership(ctx) {
					klog.Infof("Leadership lost during renewal [elapsed: %s]",
						common.GetElapsedTime(ctx))

					// Get the new leader (if any)
					newLeader, err := lc.getCurrentLeader(ctx, 0)

					if err != nil {
						klog.Warningf("Could not get current leader after losing during renewal: %s [elapsed: %s]",
							err, common.GetElapsedTime(ctx))
					} else {
						lc.resetDBFailures()
					}
					// Update state
					lc.SetLeader(false)

					startTime := time.Now()
					lc.monitoringServer.RecordFailover(lc.id, newLeader, time.Since(startTime),
						fmt.Errorf("leadership lost during renewal"))

					// Stop workloads
					_ = lc.stopWorkloads()

					// Update monitoring
					lc.monitoringServer.UpdateLeaderStatus(false, lc.id, lc.cluster.Name)
				}
			} else {
				// First, get the current leader for proper transition tracking
				previousLeader, _ := lc.getCurrentLeader(ctx, 0)
				// If we're not the leader, try to acquire
				if lc.TryAcquireLeadership(ctx) {
					klog.Infof("Leadership acquired on cluster %s [elapsed: %s]",
						lc.cluster.Name, common.GetElapsedTime(ctx))

					// Update state
					lc.SetLeader(true)
					lc.resetDBFailures()

					// Create a new workload context
					lc.createWorkloadContext(ctx)

					// Wait for previous workloads to terminate before starting new ones
					if err := lc.waitForPreviousWorkloads(ctx); err != nil {
						klog.Warningf("Error waiting for previous workloads: %v [elapsed: %s]",
							err, common.GetElapsedTime(ctx))
					}

					// Start workloads
					if err := lc.startWorkloads(); err != nil {
						klog.Errorf("Failed to restart workload upon failover: %s [elapsed: %s]",
							err, common.GetElapsedTime(ctx))
					}

					// Update monitoring
					lc.monitoringServer.UpdateLeaderStatus(true, lc.id, lc.cluster.Name)

					// Record the leadership acquisition
					if previousLeader != "" && previousLeader != lc.id {
						lc.monitoringServer.RecordFailover(previousLeader, lc.id, 0, nil)
					}
				}
			}

			lc.workloadMutex.Unlock()
		}
	}
}

// getCurrentLeader gets the current leader's ID from storage
func (lc *LeaderController) getCurrentLeader(ctx context.Context, attempt int) (string, error) {
	startTime := time.Now()
	val, err := lc.storage.GetLockValue(ctx, lc.leaderLockKey)
	duration := time.Since(startTime)

	// Record Redis operation
	lc.metrics.RecordDBOperation("get", duration, err)

	if err != nil {
		if attempt > 3 {
			return "", err
		}
		time.Sleep(1 * time.Second)
		return lc.getCurrentLeader(ctx, attempt+1)
	}

	if val == "" {
		return "", fmt.Errorf("no leader")
	}

	return val, nil
}

// createWorkloadContext creates a new context for workloads
func (lc *LeaderController) createWorkloadContext(ctx context.Context) {
	// Cancel existing context if it exists
	if lc.workloadCancel != nil {
		lc.workloadCancel()
	}

	// Create a new context
	lc.workloadCtx, lc.workloadCancel = context.WithCancel(ctx)
}

// waitForPreviousWorkloads waits for workloads from the previous leader to terminate
func (lc *LeaderController) waitForPreviousWorkloads(ctx context.Context) error {
	// Log the current state
	klog.Infof("waitForPreviousWorkloads: current=%s, previousLeader=%s, isSelfRestart=%v",
		lc.id, lc.previousLeader, lc.isSelfRestart)

	// If this is a restart of the same leader, we can potentially reuse our own workloads
	if lc.isSelfRestart {
		klog.Infof("Self restart detected, checking for reusable workloads")

		// Check if our previous workloads are still running and in good state
		if lc.canReuseExistingWorkloads(ctx) {
			klog.Infof("Existing workloads can be reused, skipping wait and recreation")
			return nil
		}

		klog.Infof("Cannot reuse existing workloads, will wait for them to terminate [%v]",
			lc.monitoringServer.GetLeaderInfo())
	} else if lc.previousLeader == "" {
		klog.Infof("No previous leader detected, no need to wait for workloads [%v]",
			lc.monitoringServer.GetLeaderInfo())
		return nil
	} else {
		klog.Infof("Waiting for workloads from previous leader %s to terminate [%v wait %s/%s]",
			lc.previousLeader, lc.monitoringServer.GetLeaderInfo(), workloadCheckInterval, maxWorkloadWaitTime)
	}

	// Rest of the method remains the same...
	// Create a context with timeout to limit how long we'll wait
	waitCtx, cancel := context.WithTimeout(ctx, maxWorkloadWaitTime)
	defer cancel()

	// Check for workloads with the controlled-shutdown annotation
	ticker := time.NewTicker(workloadCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("timed out waiting for previous workloads to terminate [wait %s/%s]",
				workloadCheckInterval, maxWorkloadWaitTime)
		case <-ticker.C:
			// Check if there are any pods with the controlled-shutdown annotation
			pods, err := lc.GetClient().CoreV1().Pods("").List(ctx, metav1.ListOptions{
				LabelSelector: "managed-by=k8-highlander",
			})

			if err != nil {
				klog.Warningf("Error listing pods: %v", err)
				continue
			}

			// Count pods that are being shut down
			shutdownPods := 0
			klog.Infof("Shutting down pods: %d [%s/%s]", len(pods.Items), workloadCheckInterval, maxWorkloadWaitTime)

			for _, pod := range pods.Items {
				if pod.Annotations != nil {
					if _, ok := pod.Annotations[workloadShutdownAnnotation]; ok {
						shutdownPods++
					}
				}
			}

			if shutdownPods == 0 {
				klog.Infof("No pods with shutdown annotation found, proceeding with workload startup")
				return nil
			}

			klog.Infof("Waiting for %d pods with shutdown annotation to terminate", shutdownPods)
		}
	}
}

func (lc *LeaderController) GetClient() kubernetes.Interface {
	return lc.cluster.GetClient()
}

func (lc *LeaderController) SetClient(client kubernetes.Interface, dynamicClient dynamic.Interface) {
	lc.cluster.SetClient(client, dynamicClient)
}

// startWorkloads starts all workloads
func (lc *LeaderController) startWorkloads() error {
	klog.Infof("Starting workloads and CRD watcher: %s", lc.id)

	// Start workloads
	if err := lc.workloadManager.StartAll(lc.workloadCtx); err != nil {
		klog.Errorf("Failed to start workloads: %v", err)
		lc.monitoringServer.SetError(fmt.Errorf("failed to start workloads: %w", err))
		return err
	}

	// Start CRD watcher
	if lc.crdWatcher != nil {
		if err := lc.crdWatcher.Start(lc.workloadCtx); err != nil {
			klog.Errorf("Failed to start CRD watcher: %v", err)
		}
	}

	return nil
}

// stopWorkloads stops all workloads
func (lc *LeaderController) stopWorkloads() error {
	klog.Infof("Stopping workloads and CRD watcher: %s", lc.id)

	// Cancel workload context
	if lc.workloadCancel != nil {
		lc.workloadCancel()
		lc.workloadCancel = nil
	}

	// Stop workloads
	if err := lc.workloadManager.StopAll(context.Background()); err != nil {
		klog.Errorf("Failed to stop workloads: %v", err)
		lc.monitoringServer.SetError(fmt.Errorf("failed to stop workloads: %w", err))
		return err
	}

	// Stop CRD watcher
	if lc.crdWatcher != nil {
		if err := lc.crdWatcher.Stop(context.Background()); err != nil {
			klog.Errorf("Failed to stop CRD watcher: %v", err)
		}
	}
	return nil
}

// monitorWorkloads monitors workload health and restarts any unhealthy workloads
func (lc *LeaderController) monitorWorkloads(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Keep track of restart attempts to implement exponential backoff
	restartAttempts := make(map[string]int)
	lastRestartTime := make(map[string]time.Time)

	for {
		select {
		case <-ctx.Done():
			return
		case <-lc.stopCh:
			return
		case <-ticker.C:
			// Only the leader should manage workloads
			if !lc.IsLeader() {
				continue
			}

			// Check workload health
			statuses := lc.workloadManager.GetAllStatuses()
			for name, status := range statuses {
				// Only attempt recovery for active workloads that are unhealthy
				if status.Active && lc.controllerState == common.StateNormal && !status.Healthy && status.LastError != "" {
					klog.Warningf("Workload %s is unhealthy: %s", name, status.LastError)

					// Don't restart pods that are just in Pending state
					if strings.Contains(status.LastError, "Pod in Pending state") {
						klog.V(4).Infof("Workload %s is in Pending state, not restarting", name)
						continue
					}

					// Don't restart workloads that were just started
					if status.LastTransition.Add(2 * time.Minute).After(time.Now()) {
						klog.V(4).Infof("Workload %s was recently started, not restarting yet", name)
						continue
					}

					// Implement exponential backoff for restarts
					attempts := restartAttempts[name]
					lastRestart, hasRestarted := lastRestartTime[name]

					// Calculate backoff delay based on attempts (with a maximum)
					backoffDelay := time.Duration(math.Min(float64(60), math.Pow(2, float64(attempts)))) * time.Second

					// Check if we need to wait longer before retrying
					if hasRestarted && time.Since(lastRestart) < backoffDelay {
						klog.V(4).Infof("Workload %s is in backoff period (%v), not restarting yet",
							name, backoffDelay)
						continue
					}

					// Try to recover the workload
					workload, exists := lc.workloadManager.GetWorkload(name)
					if exists {
						klog.Infof("Attempting to recover workload %s (attempt %d)", name, attempts+1)

						// Stop and restart the workload
						if err := workload.Stop(ctx); err != nil {
							klog.Errorf("Failed to stop unhealthy workload %s: %v", name, err)
						}

						if err := workload.Start(ctx); err != nil {
							klog.Errorf("Failed to restart workload %s: %v", name, err)
						}

						// Update restart tracking
						restartAttempts[name] = attempts + 1
						lastRestartTime[name] = time.Now()
					}
				} else if status.Healthy {
					// Reset restart attempts when workload becomes healthy
					if _, exists := restartAttempts[name]; exists {
						klog.Infof("Workload %s is now healthy, resetting restart counter", name)
						delete(restartAttempts, name)
						delete(lastRestartTime, name)
					}
				}
			}

			// Check for orphaned workloads (workloads that should be running but aren't)
			lc.checkForOrphanedWorkloads(ctx)
		}
	}
}

// checkForOrphanedWorkloads checks for workloads that should be running but aren't
func (lc *LeaderController) checkForOrphanedWorkloads(ctx context.Context) {
	// Only proceed if we're the leader
	if !lc.IsLeader() {
		return
	}

	// Get all workload statuses
	statuses := lc.workloadManager.GetAllStatuses()

	// Check each workload type
	for name, status := range statuses {
		// Skip workloads that aren't supposed to be active
		if !status.Active {
			continue
		}

		// Check if the workload is actually running in the cluster
		switch status.Type {
		case common.WorkloadTypeProcess:
			// Check if the pod exists
			podName := fmt.Sprintf("%s-pod", name)
			_, err := lc.GetClient().CoreV1().Pods(status.Details["namespace"].(string)).
				Get(ctx, podName, metav1.GetOptions{})

			if err != nil && errors.IsNotFound(err) {
				klog.Warningf("Process pod %s not found but should be running, restarting", podName)
				lc.restartWorkload(ctx, name)
			}

		case common.WorkloadTypeCronJob:
			// Check if the cronjob exists
			_, err := lc.GetClient().BatchV1().CronJobs(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil && errors.IsNotFound(err) {
				klog.Warningf("CronJob %s not found but should be running, restarting", name)
				lc.restartWorkload(ctx, name)
			}

		case common.WorkloadTypeService:
			// Check if the deployment exists
			_, err := lc.GetClient().AppsV1().Deployments(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil && errors.IsNotFound(err) {
				klog.Warningf("Deployment %s not found but should be running, restarting", name)
				lc.restartWorkload(ctx, name)
			}

		case common.WorkloadTypePersistent:
			// Check if the statefulset exists
			_, err := lc.GetClient().AppsV1().StatefulSets(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil && errors.IsNotFound(err) {
				klog.Warningf("StatefulSet %s not found but should be running, restarting", name)
				lc.restartWorkload(ctx, name)
			}
		}
	}
}

// restartWorkload restarts a specific workload
func (lc *LeaderController) restartWorkload(ctx context.Context, name string) {
	workload, exists := lc.workloadManager.GetWorkload(name)
	if !exists {
		klog.Errorf("Workload %s not found in manager for restart (leader %v)", name, lc.monitoringServer.GetLeaderInfo())
		return
	}

	klog.Infof("Restarting workload %s (leader %v)", name, lc.monitoringServer.GetLeaderInfo())

	// Stop the workload if it's running
	if err := workload.Stop(ctx); err != nil {
		klog.Warningf("Error stopping workload %s: %v", name, err)
		// Continue anyway to try to start it
	}

	// Start the workload
	if err := workload.Start(ctx); err != nil {
		klog.Errorf("Error starting workload %s: %v", name, err)
		lc.monitoringServer.SetError(fmt.Errorf("failed to restart workload %s: %w", name, err))
	} else {
		klog.Infof("Successfully restarted workload %s (leader %v)", name, lc.monitoringServer.GetLeaderInfo())
	}
}

// checkWorkloadOwnership checks if a pod is owned by a specific leader
func (lc *LeaderController) checkWorkloadOwnership(pod *corev1.Pod, leaderID string) bool {
	if pod.Annotations == nil {
		return false
	}

	owner, exists := pod.Annotations[workloadOwnerAnnotation]
	return exists && owner == leaderID
}

// MarkWorkloadForShutdown adds the controlled-shutdown annotation to a pod - TODO not used
func (lc *LeaderController) MarkWorkloadForShutdown(ctx context.Context, pod *corev1.Pod) error {
	// Create a copy of the pod to modify
	podCopy := pod.DeepCopy()

	// Add the shutdown annotation if it doesn't exist
	if podCopy.Annotations == nil {
		podCopy.Annotations = make(map[string]string)
	}

	if _, exists := podCopy.Annotations[workloadShutdownAnnotation]; exists {
		// Already marked for shutdown
		return nil
	}

	// Add the annotation
	podCopy.Annotations[workloadShutdownAnnotation] = "true"

	// Update the pod
	_, err := lc.GetClient().CoreV1().Pods(pod.Namespace).Update(ctx, podCopy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to mark pod %s/%s for shutdown: %w", pod.Namespace, pod.Name, err)
	}

	klog.Infof("Marked pod %s/%s for controlled shutdown (leader %v)", pod.Namespace, pod.Name,
		lc.monitoringServer.GetLeaderInfo())
	return nil
}

func (lc *LeaderController) canReuseExistingWorkloads(ctx context.Context) bool {
	// Get all workload statuses
	statuses := lc.workloadManager.GetAllStatuses()

	// Check if all active workloads are running in the cluster
	for name, status := range statuses {
		if !status.Active {
			continue
		}

		// Check if the workload exists and is healthy
		switch status.Type {
		case common.WorkloadTypeProcess:
			podName := fmt.Sprintf("%s-pod", name)
			pod, err := lc.GetClient().CoreV1().Pods(status.Details["namespace"].(string)).
				Get(ctx, podName, metav1.GetOptions{})

			if err != nil || pod.Status.Phase != corev1.PodRunning {
				klog.Infof("Cannot reuse workloads: pod %s not running", podName)
				return false
			}

			// Check if the pod is owned by us
			if !lc.checkWorkloadOwnership(pod, lc.id) {
				klog.Infof("Cannot reuse workloads: pod %s not owned by us", podName)
				return false
			}

		// Similar checks for other workload types...
		case common.WorkloadTypeCronJob:
			// Check if cronjob exists and is not suspended
			cronjob, err := lc.GetClient().BatchV1().CronJobs(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil || (cronjob.Spec.Suspend != nil && *cronjob.Spec.Suspend) {
				return false
			}

		case common.WorkloadTypeService:
			// Check if deployment exists and has correct replicas
			deployment, err := lc.GetClient().AppsV1().Deployments(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil || (deployment.Spec.Replicas != nil && *deployment.Spec.Replicas == 0) {
				return false
			}

		case common.WorkloadTypePersistent:
			// Check if statefulset exists and has correct replicas
			statefulset, err := lc.GetClient().AppsV1().StatefulSets(status.Details["namespace"].(string)).
				Get(ctx, name, metav1.GetOptions{})

			if err != nil || (statefulset.Spec.Replicas != nil && *statefulset.Spec.Replicas == 0) {
				return false
			}
		}
	}

	// If we get here, all workloads are running and can be reused
	return true
}

// Enhanced isConnectionError function that works for both Redis and relational databases
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific error types
	errStr := err.Error()

	// Common connection errors for both Redis and relational databases
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "i/o timeout") ||
		strings.Contains(errStr, "connection reset by peer") ||
		strings.Contains(errStr, "network is unreachable") ||
		strings.Contains(errStr, "connection timed out") ||
		strings.Contains(errStr, "dial tcp") {
		return true
	}

	// Redis-specific connection errors
	if strings.Contains(errStr, "redis: client is closed") ||
		strings.Contains(errStr, "redis: connection pool timeout") ||
		strings.Contains(errStr, "redis server: connection reset") ||
		strings.Contains(errStr, "redis server: connection refused") ||
		strings.Contains(errStr, "redis server: connection timed out") {
		return true
	}

	// PostgreSQL-specific connection errors
	if strings.Contains(errStr, "pq: server closed the connection") ||
		strings.Contains(errStr, "failed to connect to") ||
		strings.Contains(errStr, "database connection failed") ||
		strings.Contains(errStr, "pq: SSL connection error") {
		return true
	}

	// MySQL-specific connection errors
	if strings.Contains(errStr, "Error 1040: Too many connections") ||
		strings.Contains(errStr, "Error 2002: Can't connect") ||
		strings.Contains(errStr, "Error 2003: Can't connect") ||
		strings.Contains(errStr, "Error 2005: Unknown MySQL server host") ||
		strings.Contains(errStr, "Error 2006: MySQL server has gone away") ||
		strings.Contains(errStr, "Error 2013: Lost connection") {
		return true
	}

	// SQLite-specific connection errors
	if strings.Contains(errStr, "database is locked") ||
		strings.Contains(errStr, "unable to open database file") {
		return true
	}

	// Generic SQL connection errors that might be thrown by different drivers
	if strings.Contains(errStr, "connection") && (strings.Contains(errStr, "lost") ||
		strings.Contains(errStr, "closed") ||
		strings.Contains(errStr, "failed") ||
		strings.Contains(errStr, "dropped") ||
		strings.Contains(errStr, "terminated") ||
		strings.Contains(errStr, "error")) {
		return true
	}

	// Handle wrapped errors
	var redisErr redis.Error
	if ierrors.As(err, &redisErr) && (strings.Contains(redisErr.Error(), "connection") ||
		strings.Contains(redisErr.Error(), "timeout") ||
		strings.Contains(redisErr.Error(), "refused")) {
		return true
	}

	// Check for context deadline errors which often indicate connection issues
	if ierrors.Is(err, context.DeadlineExceeded) {
		return true
	}

	return false
}
