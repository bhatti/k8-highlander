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

// Package workloads provides the core workload management functionality for k8-highlander.
//
// This file implements the Manager interface and its concrete implementation, which
// serves as the central coordinator for all workload types in the system. The manager
// handles the lifecycle of workloads, provides status information, and ensures proper
// handling of errors and retries during workload operations.
//
// Key features:
// - Unified management of different workload types (Process, CronJob, Service, Persistent)
// - Concurrent workload operations for performance
// - Error categorization with special handling for severe errors
// - Automatic retry of failed workloads with exponential backoff
// - Clean shutdown coordination
// - Centralized status collection for monitoring
// - Thread-safety for concurrent operations
//
// The Manager is designed to work with the leader election system, ensuring that
// workloads are only active on the elected leader node and are properly handed over
// during failover events. This is critical for maintaining the singleton behavior
// that k8-highlander provides.

package workloads

import (
	"context"
	ierrors "errors"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"math"
	"reflect"
	"sync"
	"time"
)

// Manager defines the interface for managing workloads in k8-highlander.
// It provides methods for starting, stopping, adding, removing, and querying
// workloads of various types. This interface allows for different implementations
// of workload management while maintaining a consistent API.
type Manager interface {
	// StartAll starts all workloads
	StartAll(ctx context.Context, client kubernetes.Interface) error

	// StopAll stops all workloads
	StopAll(ctx context.Context) error

	// AddWorkload adds a workload to the manager
	AddWorkload(workload common.Workload) error

	// RemoveWorkload removes a workload from the manager
	RemoveWorkload(name string) error

	// GetWorkload gets a workload by name
	GetWorkload(name string) (common.Workload, bool)

	// GetAllWorkloads gets all workloads
	GetAllWorkloads() map[string]common.Workload

	// GetAllStatuses gets the status of all workloads
	GetAllStatuses() map[string]common.WorkloadStatus

	// Cleanup performs cleanup when the manager is being shut down
	Cleanup()
}

// ManagerImpl implements the Manager interface
type ManagerImpl struct {
	workloads        map[string]common.Workload
	workloadMutex    sync.RWMutex
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
	failedWorkloads  map[string]failedWorkloadInfo
	failedMutex      sync.RWMutex
	retryCtx         context.Context
	retryCancel      context.CancelFunc
	isShuttingDown   bool
	shutdownMutex    sync.RWMutex
}

// failedWorkloadInfo tracks information about a failed workload
type failedWorkloadInfo struct {
	workload     common.Workload
	lastAttempt  time.Time
	failureCount int
	lastError    error
}

// NewManager creates a new workload manager
func NewManager(metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) *ManagerImpl {
	ctx, cancel := context.WithCancel(context.Background())

	return &ManagerImpl{
		workloads:        make(map[string]common.Workload),
		metrics:          metrics,
		monitoringServer: monitoringServer,
		failedWorkloads:  make(map[string]failedWorkloadInfo),
		retryCtx:         ctx,
		retryCancel:      cancel,
		isShuttingDown:   false,
	}
}

// StartAll starts all workloads
func (m *ManagerImpl) StartAll(ctx context.Context, client kubernetes.Interface) error {
	m.shutdownMutex.Lock()
	m.isShuttingDown = false
	m.shutdownMutex.Unlock()

	m.workloadMutex.RLock()
	defer m.workloadMutex.RUnlock()

	var wg sync.WaitGroup
	errChan := make(chan error, len(m.workloads))

	// Clear the failed workloads map before starting
	m.failedMutex.Lock()
	m.failedWorkloads = make(map[string]failedWorkloadInfo)
	m.failedMutex.Unlock()

	// Start the retry background process if not already running
	go m.retryFailedWorkloads(client)

	for name, workload := range m.workloads {
		wg.Add(1)
		go func(name string, w common.Workload) {
			defer wg.Done()

			startTime := time.Now()
			err := w.Start(ctx, client)
			duration := time.Since(startTime)

			if m.metrics != nil {
				m.metrics.RecordWorkloadOperation(string(w.GetType()), name, "startup", duration, err)
			}

			if err != nil {
				klog.Errorf("Failed to start workload %s: (%s) %v", name, reflect.TypeOf(err).String(), err)

				// Add to failed workloads for retry
				m.failedMutex.Lock()
				m.failedWorkloads[name] = failedWorkloadInfo{
					workload:     w,
					lastAttempt:  time.Now(),
					failureCount: 1,
					lastError:    err,
				}
				m.failedMutex.Unlock()

				errChan <- err
			} else {
				klog.Infof("Successfully started workload %s [%v]",
					name, m.monitoringServer.GetLeaderInfo())
			}
		}(name, workload)
	}

	// Wait for all workloads to start
	wg.Wait()
	close(errChan)

	// Collect errors
	var severeErrors []error
	var startErrors []error
	for err := range errChan {
		if ierrors.Is(err, &common.SevereError{}) {
			severeErrors = append(severeErrors, err)
		} else {
			startErrors = append(startErrors, err)
		}
	}

	if len(severeErrors) > 0 {
		return common.NewSevereErrorMessage("ManagerImpl",
			fmt.Sprintf("manager encountered severe errors during startup: %v", ierrors.Join(severeErrors...)))
	}

	// For non-severe errors, we'll retry in the background, but still report them
	if len(startErrors) > 0 {
		return fmt.Errorf("manager failed with non-severe to start some workloads: %v", ierrors.Join(startErrors...))
	}

	return nil
}

// StopAll stops all workloads
func (m *ManagerImpl) StopAll(ctx context.Context) error {
	klog.Infof("Starting to stop workloads")

	// Set shutdown flag to prevent retries
	m.shutdownMutex.Lock()
	m.isShuttingDown = true
	m.shutdownMutex.Unlock()

	m.workloadMutex.RLock()
	defer m.workloadMutex.RUnlock()

	var wg sync.WaitGroup
	errChan := make(chan error, len(m.workloads))

	for name, workload := range m.workloads {
		wg.Add(1)
		go func(name string, w common.Workload) {
			defer wg.Done()

			startTime := time.Now()
			err := w.Stop(ctx)
			duration := time.Since(startTime)

			if m.metrics != nil {
				m.metrics.RecordWorkloadOperation(string(w.GetType()), name, "shutdown", duration, err)
			}

			if err != nil {
				klog.Errorf("Failed to stop workload %s: %v", name, err)
				errChan <- fmt.Errorf("failed to stop workload %s: %w", name, err)
			} else {
				klog.Infof("Successfully stopped workload %s [%v]", name, m.monitoringServer.GetLeaderInfo())
			}

		}(name, workload)
	}

	// Wait for all workloads to stop
	wg.Wait()
	close(errChan)

	// Collect errors
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("failed to stop some workloads: %v [%v]", errs, m.monitoringServer.GetLeaderInfo())
	}

	klog.Infof("Finished stopping workloads [%v]", m.monitoringServer.GetLeaderInfo())

	return nil
}

// Cleanup performs cleanup when the manager is being shut down
func (m *ManagerImpl) Cleanup() {
	// Cancel the retry context to stop the retry process
	if m.retryCancel != nil {
		m.retryCancel()
	}

	// Clear the failed workloads map
	m.failedMutex.Lock()
	m.failedWorkloads = make(map[string]failedWorkloadInfo)
	m.failedMutex.Unlock()
}

// AddWorkload adds a workload to the manager
func (m *ManagerImpl) AddWorkload(workload common.Workload) error {
	m.workloadMutex.Lock()
	defer m.workloadMutex.Unlock()

	if _, exists := m.workloads[workload.GetName()]; exists {
		return fmt.Errorf("workload %s already exists", workload.GetName())
	}

	// If the workload supports metrics, set them
	if monitoringSetter, ok := workload.(interface {
		SetMonitoring(*monitoring.ControllerMetrics, *monitoring.MonitoringServer)
	}); ok {
		monitoringSetter.SetMonitoring(m.metrics, m.monitoringServer)
	}

	m.workloads[workload.GetName()] = workload
	return nil
}

// RemoveWorkload removes a workload from the manager
func (m *ManagerImpl) RemoveWorkload(name string) error {
	m.workloadMutex.Lock()
	defer m.workloadMutex.Unlock()

	if _, exists := m.workloads[name]; !exists {
		return fmt.Errorf("workload %s does not exist", name)
	}

	delete(m.workloads, name)
	return nil
}

// GetWorkload gets a workload by name
func (m *ManagerImpl) GetWorkload(name string) (common.Workload, bool) {
	m.workloadMutex.RLock()
	defer m.workloadMutex.RUnlock()

	workload, exists := m.workloads[name]
	return workload, exists
}

// GetAllWorkloads gets all workloads
func (m *ManagerImpl) GetAllWorkloads() map[string]common.Workload {
	m.workloadMutex.RLock()
	defer m.workloadMutex.RUnlock()

	result := make(map[string]common.Workload, len(m.workloads))
	for name, workload := range m.workloads {
		result[name] = workload
	}

	return result
}

// GetAllStatuses gets the status of all workloads
func (m *ManagerImpl) GetAllStatuses() map[string]common.WorkloadStatus {
	m.workloadMutex.RLock()
	defer m.workloadMutex.RUnlock()

	result := make(map[string]common.WorkloadStatus, len(m.workloads))
	for name, workload := range m.workloads {
		result[name] = workload.GetStatus()
	}

	return result
}

func RetryKubernetesOperation(operation func() error) error {
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 100 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
	}

	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := operation()
		if err == nil {
			return true, nil
		}

		// Determine if we should retry based on the error
		if errors.IsServerTimeout(err) || errors.IsTimeout(err) ||
			errors.IsTooManyRequests(err) || errors.IsServiceUnavailable(err) {
			return false, nil // Retry
		}

		return false, err // Don't retry
	})
}

// retryFailedWorkloads periodically retries failed workloads
func (m *ManagerImpl) retryFailedWorkloads(client kubernetes.Interface) {
	// Use exponential backoff for retries
	initialBackoff := 5 * time.Second
	maxBackoff := 5 * time.Minute
	backoffFactor := 2.0

	ticker := time.NewTicker(10 * time.Second) // Check every 10 seconds
	defer ticker.Stop()

	for {
		select {
		case <-m.retryCtx.Done():
			klog.Info("Stopping workload retry process")
			return
		case <-ticker.C:
			// Check if we're shutting down
			m.shutdownMutex.RLock()
			shuttingDown := m.isShuttingDown
			m.shutdownMutex.RUnlock()

			if shuttingDown {
				continue // Skip retries during shutdown
			}

			// Get a copy of failed workloads to retry
			m.failedMutex.Lock()
			workloadsToRetry := make(map[string]failedWorkloadInfo)
			for name, info := range m.failedWorkloads {
				workloadsToRetry[name] = info
			}
			m.failedMutex.Unlock()

			// Retry each failed workload if enough time has passed
			for name, info := range workloadsToRetry {
				if m.metrics != nil {
					m.metrics.WorkloadRetryAttempts.WithLabelValues(string(info.workload.GetType()), name).Inc()
				}

				// Calculate backoff time based on failure count
				backoff := time.Duration(float64(initialBackoff) * math.Pow(backoffFactor, float64(info.failureCount-1)))
				if backoff > maxBackoff {
					backoff = maxBackoff
				}

				// Check if enough time has passed since the last attempt
				if time.Since(info.lastAttempt) < backoff {
					continue // Not time to retry yet
				}

				klog.Infof("Retrying workload %s (attempt %d) after %v", name, info.failureCount+1, backoff)

				// Create a context with timeout for the retry
				retryCtx, cancel := context.WithTimeout(m.retryCtx, 30*time.Second)

				// Attempt to start the workload
				startTime := time.Now()
				err := info.workload.Start(retryCtx, client)
				duration := time.Since(startTime)

				// Record metrics
				if m.metrics != nil {
					m.metrics.RecordWorkloadOperation(string(info.workload.GetType()), name, "retry", duration, err)
				}

				if err != nil {
					// Update failure info
					klog.Errorf("Failed to retry workload %s (attempt %d): %v", name, info.failureCount+1, err)

					m.failedMutex.Lock()
					m.failedWorkloads[name] = failedWorkloadInfo{
						workload:     info.workload,
						lastAttempt:  time.Now(),
						failureCount: info.failureCount + 1,
						lastError:    err,
					}
					m.failedMutex.Unlock()
					if m.monitoringServer != nil {
						// Report the failed workload status
						m.monitoringServer.UpdateWorkloadRetryStatus(
							string(info.workload.GetType()),
							name,
							info.failureCount,
							info.lastError.Error(),
							backoff,
						)
					}
				} else {
					// Workload started successfully, remove from failed list
					klog.Infof("Successfully restarted workload %s after %d attempts", name, info.failureCount+1)

					if m.metrics != nil {
						m.metrics.WorkloadRetrySuccess.WithLabelValues(string(info.workload.GetType()), name).Inc()
					}
					m.failedMutex.Lock()
					delete(m.failedWorkloads, name)
					m.failedMutex.Unlock()
				}

				cancel() // Clean up the retry context
			}
		}
	}
}
