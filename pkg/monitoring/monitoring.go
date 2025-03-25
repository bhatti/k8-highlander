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

// Package monitoring provides comprehensive metrics and health monitoring for k8-highlander.
//
// This package implements a complete monitoring system using Prometheus metrics
// and structured health status reporting. It tracks various aspects of the system's
// operation including leadership state, workload health, database operations,
// and failover events. The monitoring data is exposed through Prometheus metrics
// and a health status API that can be used for dashboards and monitoring tools.
//
// Key components:
// - Prometheus metrics collection for all system aspects
// - Health status tracking and reporting
// - Error classification and categorization
// - Leadership transition tracking
// - Workload status monitoring
// - Database operation metrics
// - System resource monitoring
//
// The monitoring system is designed to provide observability into the distributed
// nature of k8-highlander, helping operators identify issues, track performance,
// and understand the system's behavior during normal operation and failover events.

package monitoring

import (
	"context"
	"errors"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/go-redis/redis/v8"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/klog/v2"
)

// ControllerMetrics contains all Prometheus metrics for the controller.
// It defines gauges, counters, and histograms for tracking various aspects
// of system performance and state.
type ControllerMetrics struct {
	// Leadership metrics
	IsLeader                 prometheus.Gauge
	LeadershipTransitions    prometheus.Counter
	LeadershipDuration       prometheus.Histogram
	LeadershipAcquisitions   prometheus.Counter
	LeadershipLosses         prometheus.Counter
	LeadershipFailedAttempts prometheus.Counter
	LastLeaderTransition     prometheus.Gauge

	// Workload metrics
	WorkloadStatus           *prometheus.GaugeVec
	WorkloadStartupDuration  *prometheus.HistogramVec
	WorkloadShutdownDuration *prometheus.HistogramVec
	WorkloadOperations       *prometheus.CounterVec
	WorkloadErrors           *prometheus.CounterVec

	// Failover metrics
	FailoverDuration prometheus.Histogram
	FailoverCount    prometheus.Counter
	FailoverErrors   prometheus.Counter

	// DB metrics
	DBOperations *prometheus.CounterVec
	DBErrors     *prometheus.CounterVec
	DBLatency    *prometheus.HistogramVec
	DBLockTTL    prometheus.Gauge

	// Cluster metrics
	ClusterHealth      *prometheus.GaugeVec
	ClusterPriority    *prometheus.GaugeVec
	ClusterTransitions *prometheus.CounterVec

	// System metrics
	SystemLoad     prometheus.Gauge
	GoroutineCount prometheus.Gauge
	MemoryUsage    prometheus.Gauge

	// Custom metrics for specific workloads
	ProcessStatus    *prometheus.GaugeVec
	ServiceStatus    *prometheus.GaugeVec
	PersistentStatus *prometheus.GaugeVec
	CronJobStatus    *prometheus.GaugeVec

	WorkloadRetryAttempts *prometheus.CounterVec
	WorkloadRetrySuccess  *prometheus.CounterVec

	DBConnectionFailed     prometheus.Counter
	DBConsecutiveFailures  prometheus.Gauge
	DBFailureDuration      prometheus.Gauge
	DBLastSuccessTimestamp prometheus.Gauge
	DBReconnectionAttempts prometheus.Counter
	DBReconnectionSuccess  prometheus.Counter
	ControllerStateStatus  *prometheus.GaugeVec
	SplitBrainProtection   prometheus.Gauge
}

// HealthStatus represents the health status of the controller
type HealthStatus struct {
	IsLeader             bool          `json:"isLeader"`
	LeaderSince          time.Time     `json:"leaderSince,omitempty"`
	LastLeaderTransition time.Time     `json:"lastLeaderTransition"`
	Uptime               time.Duration `json:"uptime"`
	LeaderID             string        `json:"leaderID"`
	ClusterName          string        `json:"clusterName"`
	ClusterHealth        bool          `json:"clusterHealth"`
	DBConnected          bool          `json:"dbConnected"`
	StorageType          string        `json:"storageType"`

	WorkloadStatus        map[string]interface{} `json:"workloadStatus"`
	ReadyForTraffic       bool                   `json:"readyForTraffic"`
	Version               string                 `json:"version"`
	BuildInfo             string                 `json:"buildInfo"`
	LastError             string                 `json:"lastError,omitempty"`
	FailoverCount         int                    `json:"failoverCount"`
	LeadershipTransitions int                    `json:"leadershipTransitions"`

	CurrentLeader              string `json:"currentLeader"`
	LastLeader                 string `json:"lastLeader"`
	LastLeadershipChangeReason string `json:"lastLeadershipChangeReason"`

	ControllerState      string        `json:"controllerState"`
	DBFailureCount       int           `json:"dbFailureCount"`
	DBFailureDuration    time.Duration `json:"dbFailureDuration,omitempty"`
	DBLastSuccessTime    time.Time     `json:"dbLastSuccessTime"`
	DBLastFailureTime    time.Time     `json:"dbLastFailureTime,omitempty"`
	DBFailureThreshold   time.Duration `json:"dbFailureThreshold"`
	SplitBrainProtection bool          `json:"splitBrainProtection"`
}

// MonitoringServer provides monitoring and health endpoints
type MonitoringServer struct {
	metrics        *ControllerMetrics
	healthStatus   HealthStatus
	statusMutex    sync.RWMutex
	startTime      time.Time
	updateInterval time.Duration
	version        string
	buildInfo      string
	stopCh         chan struct{}
}

// NewControllerMetrics creates and registers all metrics
func NewControllerMetrics(reg prometheus.Registerer) *ControllerMetrics {
	if reg == nil {
		reg = prometheus.DefaultRegisterer
	}

	metrics := &ControllerMetrics{
		// Leadership metrics
		IsLeader: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_is_leader",
			Help: "Indicates if this instance is currently the leader (1) or not (0)",
		}),
		LeadershipTransitions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_leadership_transitions_total",
			Help: "Total number of leadership transitions",
		}),
		LeadershipDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "k8_highlander_leadership_duration_seconds",
			Help:    "Duration of leadership periods",
			Buckets: prometheus.ExponentialBuckets(10, 2, 10), // 10s to ~2.8 hours
		}),
		LeadershipAcquisitions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_leadership_acquisitions_total",
			Help: "Total number of leadership acquisitions",
		}),
		LeadershipLosses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_leadership_losses_total",
			Help: "Total number of leadership losses",
		}),
		LeadershipFailedAttempts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_leadership_failed_attempts_total",
			Help: "Total number of failed leadership acquisition attempts",
		}),
		LastLeaderTransition: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_last_leader_transition_timestamp_seconds",
			Help: "Timestamp of the last leadership transition",
		}),

		// Workload metrics
		WorkloadStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_workload_status",
			Help: "Status of managed workloads (1=active, 0=inactive)",
		}, []string{"type", "name", "namespace"}),
		WorkloadStartupDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "k8_highlander_workload_startup_duration_seconds",
			Help:    "Time taken to start workloads",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1s to ~51s
		}, []string{"type", "name"}),
		WorkloadShutdownDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "k8_highlander_workload_shutdown_duration_seconds",
			Help:    "Time taken to shut down workloads",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10), // 0.1s to ~51s
		}, []string{"type", "name"}),
		WorkloadOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_workload_operations_total",
			Help: "Total number of operations performed on workloads",
		}, []string{"type", "name", "operation"}),
		WorkloadErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_workload_errors_total",
			Help: "Total number of errors encountered with workloads",
		}, []string{"type", "name", "error_type"}),

		// Failover metrics
		FailoverDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "k8_highlander_failover_duration_seconds",
			Help:    "Time taken to complete a failover",
			Buckets: prometheus.LinearBuckets(1, 5, 10), // 1s to 50s
		}),
		FailoverCount: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_failover_total",
			Help: "Total number of failovers",
		}),
		FailoverErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_failover_errors_total",
			Help: "Total number of failover errors",
		}),

		// DB metrics
		DBOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_db_operations_total",
			Help: "Total number of DB operations",
		}, []string{"operation"}),
		DBErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_db_errors_total",
			Help: "Total number of DB errors",
		}, []string{"operation", "error_type"}),
		DBLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "k8_highlander_db_latency_seconds",
			Help:    "Latency of DB operations",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~0.5s
		}, []string{"operation"}),
		DBLockTTL: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_db_lock_ttl_seconds",
			Help: "TTL of the DB lock in seconds",
		}),

		// Cluster metrics
		ClusterHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_cluster_health",
			Help: "Health status of clusters (1=healthy, 0=unhealthy)",
		}, []string{"cluster"}),
		ClusterPriority: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_cluster_priority",
			Help: "Priority of clusters (lower is higher priority)",
		}, []string{"cluster"}),
		ClusterTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_cluster_transitions_total",
			Help: "Total number of transitions between clusters",
		}, []string{"from_cluster", "to_cluster"}),

		// System metrics
		SystemLoad: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_system_load",
			Help: "System load average",
		}),
		GoroutineCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_goroutine_count",
			Help: "Number of goroutines",
		}),
		MemoryUsage: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_memory_usage_bytes",
			Help: "Memory usage in bytes",
		}),

		// Custom metrics for specific workloads
		CronJobStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_cronjob_status",
			Help: "Status of managed cron jobs (1=active, 0=suspended)",
		}, []string{"name", "namespace"}),
		ProcessStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_process_status",
			Help: "Status of managed process jobs (1=active, 0=suspended)",
		}, []string{"name", "namespace"}),
		ServiceStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_service_status",
			Help: "Status of managed service jobs (1=active, 0=suspended)",
		}, []string{"name", "namespace"}),
		PersistentStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_persistent_status",
			Help: "Status of managed persistent jobs (1=active, 0=suspended)",
		}, []string{"name", "namespace"}),
		WorkloadRetryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_workload_retry_attempts_total",
			Help: "Total number of retry attempts for workloads",
		}, []string{"type", "name"}),
		WorkloadRetrySuccess: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "k8_highlander_workload_retry_success_total",
			Help: "Total number of successful retry attempts for workloads",
		}, []string{"type", "name"}),
		DBConnectionFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_db_connection_failed_total",
			Help: "Total number of DB connection failures",
		}),
		DBConsecutiveFailures: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_db_consecutive_failures",
			Help: "Current number of consecutive DB failures",
		}),
		DBFailureDuration: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_db_failure_duration_seconds",
			Help: "Duration of the current DB failure in seconds",
		}),
		DBLastSuccessTimestamp: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_db_last_success_timestamp_seconds",
			Help: "Timestamp of the last successful DB operation",
		}),
		DBReconnectionAttempts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_db_reconnection_attempts_total",
			Help: "Total number of DB reconnection attempts",
		}),
		DBReconnectionSuccess: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "k8_highlander_db_reconnection_success_total",
			Help: "Total number of successful DB reconnections",
		}),
		ControllerStateStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "k8_highlander_controller_state_status",
			Help: "Status of the controller state (1=active, 0=inactive)",
		}, []string{"state"}),
		SplitBrainProtection: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "k8_highlander_split_brain_protection_active",
			Help: "Indicates if split-brain protection has been activated (1) or not (0)",
		}),
	}

	// Register all metrics with Prometheus
	reg.MustRegister(
		metrics.IsLeader,
		metrics.LeadershipTransitions,
		metrics.LeadershipDuration,
		metrics.LeadershipAcquisitions,
		metrics.LeadershipLosses,
		metrics.LeadershipFailedAttempts,
		metrics.LastLeaderTransition,
		metrics.WorkloadStatus,
		metrics.WorkloadStartupDuration,
		metrics.WorkloadShutdownDuration,
		metrics.WorkloadOperations,
		metrics.WorkloadErrors,
		metrics.FailoverDuration,
		metrics.FailoverCount,
		metrics.FailoverErrors,
		metrics.DBOperations,
		metrics.DBErrors,
		metrics.DBLatency,
		metrics.DBLockTTL,
		metrics.ClusterHealth,
		metrics.ClusterPriority,
		metrics.ClusterTransitions,
		metrics.SystemLoad,
		metrics.GoroutineCount,
		metrics.MemoryUsage,
		metrics.CronJobStatus,
		metrics.ProcessStatus,
		metrics.ServiceStatus,
		metrics.PersistentStatus,
		metrics.WorkloadRetryAttempts,
		metrics.WorkloadRetrySuccess,
		metrics.DBConnectionFailed,
		metrics.DBConsecutiveFailures,
		metrics.DBFailureDuration,
		metrics.DBLastSuccessTimestamp,
		metrics.DBReconnectionAttempts,
		metrics.DBReconnectionSuccess,
		metrics.ControllerStateStatus,
		metrics.SplitBrainProtection,
	)

	return metrics
}

// NewMonitoringServer creates a new monitoring server
func NewMonitoringServer(metrics *ControllerMetrics, version, buildInfo string) *MonitoringServer {
	return &MonitoringServer{
		metrics:        metrics,
		startTime:      time.Now(),
		updateInterval: 15 * time.Second,
		version:        version,
		buildInfo:      buildInfo,
		stopCh:         make(chan struct{}),
		healthStatus: HealthStatus{
			IsLeader:        false,
			Uptime:          0,
			ReadyForTraffic: false,
			Version:         version,
			BuildInfo:       buildInfo,
			WorkloadStatus:  make(map[string]interface{}),
		},
	}
}

// Start starts the monitoring server
func (m *MonitoringServer) Start(ctx context.Context) error {
	// Start the status updater
	go m.runStatusUpdater(ctx)

	return nil
}

// Stop stops the monitoring server
func (m *MonitoringServer) Stop(_ context.Context) error {
	close(m.stopCh)
	return nil
}

// GetHealthStatus returns a copy of the current health status
func (m *MonitoringServer) GetHealthStatus() HealthStatus {
	m.statusMutex.RLock()
	defer m.statusMutex.RUnlock()

	// Create a copy of the status
	status := m.healthStatus

	// Update uptime
	status.Uptime = time.Since(m.startTime)

	return status
}

// RecordWorkloadOperation records metrics for a workload operation
func (m *ControllerMetrics) RecordWorkloadOperation(workloadType, name, operation string, duration time.Duration, err error) {
	// Increment the operation counter
	m.WorkloadOperations.WithLabelValues(workloadType, name, operation).Inc()

	// Record the duration based on operation type
	switch operation {
	case "startup":
		m.WorkloadStartupDuration.WithLabelValues(workloadType, name).Observe(duration.Seconds())
	case "shutdown":
		m.WorkloadShutdownDuration.WithLabelValues(workloadType, name).Observe(duration.Seconds())
	}

	// If there was an error, record it
	if err != nil {
		errorType := "unknown"

		// Categorize common errors
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			errorType = "timeout"
		case errors.Is(err, context.Canceled):
			errorType = "canceled"
		default:
			// Try to categorize based on error message
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "not found"):
				errorType = "not_found"
			case strings.Contains(errMsg, "timeout"):
				errorType = "timeout"
			case strings.Contains(errMsg, "already exists"):
				errorType = "already_exists"
			}
		}

		// Increment the error counter with the appropriate labels
		m.WorkloadErrors.WithLabelValues(workloadType, name, errorType).Inc()
	}
}

// RecordDBOperation records metrics for a db operation
func (m *ControllerMetrics) RecordDBOperation(operation string, duration time.Duration, err error) {
	// Increment the operation counter
	m.DBOperations.WithLabelValues(operation).Inc()

	// Record the latency
	m.DBLatency.WithLabelValues(operation).Observe(duration.Seconds())

	// If there was an error, record it
	if err != nil {
		errorType := "unknown"

		// Categorize common DB errors
		switch {
		case errors.Is(err, redis.Nil):
			errorType = "nil"
		case errors.Is(err, context.DeadlineExceeded):
			errorType = "timeout"
		case errors.Is(err, context.Canceled):
			errorType = "canceled"
		default:
			// Try to categorize based on error message
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "connection refused"):
				errorType = "connection_refused"
			case strings.Contains(errMsg, "timeout"):
				errorType = "timeout"
			case strings.Contains(errMsg, "network"):
				errorType = "network"
			}
		}

		// Increment the error counter with the appropriate labels
		m.DBErrors.WithLabelValues(operation, errorType).Inc()
	}
}

func (m *MonitoringServer) GetLeaderInfo() common.LocalLeaderInfo {
	m.statusMutex.RLock()
	defer m.statusMutex.RUnlock()
	return common.LocalLeaderInfo{
		IsLeader:    m.healthStatus.IsLeader,
		LeaderID:    m.healthStatus.LeaderID,
		ClusterName: m.healthStatus.ClusterName,
	}
}

// UpdateLeaderStatus updates the leader status
func (m *MonitoringServer) UpdateLeaderStatus(isLeader bool, leaderID string, clusterName string) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	wasLeader := m.healthStatus.IsLeader

	// Update leader status
	m.healthStatus.IsLeader = isLeader
	m.healthStatus.LeaderID = leaderID
	m.healthStatus.ClusterName = clusterName

	// If leadership status changed, update transition time and count
	if wasLeader != isLeader {
		now := time.Now()
		m.healthStatus.LastLeaderTransition = now

		if isLeader {
			m.healthStatus.LeaderSince = now
			m.healthStatus.LeadershipTransitions++

			// Update metrics
			m.metrics.LeadershipAcquisitions.Inc()
			m.metrics.LeadershipTransitions.Inc()
			m.metrics.LastLeaderTransition.Set(float64(now.Unix()))
		} else {
			// Calculate leadership duration if we're losing leadership
			if !m.healthStatus.LeaderSince.IsZero() {
				duration := now.Sub(m.healthStatus.LeaderSince).Seconds()
				m.metrics.LeadershipDuration.Observe(duration)
			}

			// Update metrics
			m.metrics.LeadershipLosses.Inc()
			m.metrics.LeadershipTransitions.Inc()
			m.metrics.LastLeaderTransition.Set(float64(now.Unix()))
		}
	}

	// Update leader gauge
	if isLeader {
		m.metrics.IsLeader.Set(1)
	} else {
		m.metrics.IsLeader.Set(0)
	}

	// Determine if we're ready for traffic
	// We're ready if:
	// 1. We're the leader and have been for at least 5 seconds, or
	// 2. We're not the leader but the last transition was more than 30 seconds ago
	if isLeader {
		// If we just became leader, we need a grace period
		if wasLeader != isLeader {
			m.healthStatus.ReadyForTraffic = false
		} else if time.Since(m.healthStatus.LeaderSince) > 5*time.Second {
			m.healthStatus.ReadyForTraffic = true
		}
	} else {
		// If we're not the leader, we're only ready after a stabilization period
		m.healthStatus.ReadyForTraffic = time.Since(m.healthStatus.LastLeaderTransition) > 30*time.Second
	}
}

// UpdateClusterHealth updates the cluster health status
func (m *MonitoringServer) UpdateClusterHealth(clusterName string, isHealthy bool) {
	// Update metrics
	if isHealthy {
		m.metrics.ClusterHealth.WithLabelValues(clusterName).Set(1)
	} else {
		m.metrics.ClusterHealth.WithLabelValues(clusterName).Set(0)
	}

	// Update health status
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	if clusterName == m.healthStatus.ClusterName {
		m.healthStatus.ClusterHealth = isHealthy
	}
}

// UpdateDBStatus updates the DB connection status
func (m *MonitoringServer) UpdateDBStatus(isConnected bool, lockTTL float64) {
	// Update metrics
	m.metrics.DBLockTTL.Set(lockTTL)

	// Update health status
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	m.healthStatus.DBConnected = isConnected
}

// RecordDBOperation records a db operation
func (m *MonitoringServer) RecordDBOperation(operation string, duration time.Duration, err error) {
	// Update metrics
	m.metrics.DBOperations.WithLabelValues(operation).Inc()
	m.metrics.DBLatency.WithLabelValues(operation).Observe(duration.Seconds())

	if err != nil {
		errorType := "unknown"
		if err.Error() == "context deadline exceeded" {
			errorType = "timeout"
		} else if err.Error() == "connection refused" {
			errorType = "connection_refused"
		}

		m.metrics.DBErrors.WithLabelValues(operation, errorType).Inc()

		// Update health status with the error
		m.statusMutex.Lock()
		defer m.statusMutex.Unlock()

		m.healthStatus.LastError = fmt.Sprintf("DB error: %v", err)
	}
}

// UpdateWorkloadStatus updates the status of a workload
func (m *MonitoringServer) UpdateWorkloadStatus(workloadType, name, namespace string, isActive bool) {
	// Update metrics
	if isActive {
		m.metrics.WorkloadStatus.WithLabelValues(workloadType, name, namespace).Set(1)
	} else {
		m.metrics.WorkloadStatus.WithLabelValues(workloadType, name, namespace).Set(0)
	}

	// Update specific workload metrics
	switch workloadType {
	case "cronjob":
		if isActive {
			m.metrics.CronJobStatus.WithLabelValues(name, namespace).Set(1)
		} else {
			m.metrics.CronJobStatus.WithLabelValues(name, namespace).Set(0)
		}
	case "service":
		if isActive {
			m.metrics.ServiceStatus.WithLabelValues(name, namespace).Set(1)
		} else {
			m.metrics.ServiceStatus.WithLabelValues(name, namespace).Set(0)
		}
	case "process":
		if isActive {
			m.metrics.ProcessStatus.WithLabelValues(name, namespace).Set(1)
		} else {
			m.metrics.ProcessStatus.WithLabelValues(name, namespace).Set(0)
		}
	case "persistent":
		if isActive {
			m.metrics.PersistentStatus.WithLabelValues(name, namespace).Set(1)
		} else {
			m.metrics.PersistentStatus.WithLabelValues(name, namespace).Set(0)
		}
	}

	// Update health status
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	// Create nested maps if needed
	if m.healthStatus.WorkloadStatus[workloadType] == nil {
		m.healthStatus.WorkloadStatus[workloadType] = make(map[string]interface{})
	}

	workloadMap := m.healthStatus.WorkloadStatus[workloadType].(map[string]interface{})
	workloadMap[name] = map[string]interface{}{
		"active":    isActive,
		"namespace": namespace,
	}
}

// RecordFailover records a failover event
func (m *MonitoringServer) RecordFailover(fromCluster, toCluster string, duration time.Duration, err error) {
	// Update metrics
	m.metrics.FailoverCount.Inc()
	if duration > 0 {
		m.metrics.FailoverDuration.Observe(duration.Seconds())
	}
	if fromCluster != "" && toCluster != "" {
		m.metrics.ClusterTransitions.WithLabelValues(fromCluster, toCluster).Inc()
	}
	m.metrics.ClusterTransitions.WithLabelValues(fromCluster, toCluster).Inc()

	if err != nil {
		m.metrics.FailoverErrors.Inc()
	}

	// Update health status
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	m.healthStatus.FailoverCount++

	// Update leadership transition info
	if fromCluster != "" {
		m.healthStatus.LastLeader = fromCluster
	}

	if toCluster != "" {
		m.healthStatus.CurrentLeader = toCluster
	}

	if err != nil {
		m.healthStatus.LastLeadershipChangeReason = err.Error()
		m.healthStatus.LastError = fmt.Sprintf("Leadership change error: %v", err)
	} else {
		m.healthStatus.LastLeadershipChangeReason = "normal transition"
	}
}

// SetError sets an error in the health status
func (m *MonitoringServer) SetError(err error) {
	if err == nil {
		return
	}

	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	m.healthStatus.LastError = err.Error()
}

// ClearError clears the error in the health status
func (m *MonitoringServer) ClearError() {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	m.healthStatus.LastError = ""
}

// runStatusUpdater periodically updates system metrics
func (m *MonitoringServer) runStatusUpdater(ctx context.Context) {
	ticker := time.NewTicker(m.updateInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.updateSystemMetrics()
		}
	}
}

// updateSystemMetrics updates system-level metrics
func (m *MonitoringServer) updateSystemMetrics() {
	// This would be implemented to collect system metrics
	// such as CPU usage, memory usage, goroutine count, etc.

	// For demonstration purposes, we'll just set some placeholder values
	m.metrics.SystemLoad.Set(1.0)
	m.metrics.GoroutineCount.Set(100)
	m.metrics.MemoryUsage.Set(100 * 1024 * 1024) // 100 MB
}

func (m *MonitoringServer) UpdateCurrentLeader(leaderID string) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	if m.healthStatus.CurrentLeader != leaderID {
		klog.V(3).Infof("Current leader changed from %s to %s",
			m.healthStatus.CurrentLeader, leaderID)

		// Remember the previous leader
		if m.healthStatus.CurrentLeader != "" {
			m.healthStatus.LastLeader = m.healthStatus.CurrentLeader
		}

		m.healthStatus.CurrentLeader = leaderID
	}
}

// UpdateLastLeader explicitly sets the last known leader
func (m *MonitoringServer) UpdateLastLeader(leaderID string) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	m.healthStatus.LastLeader = leaderID
}

func (m *MonitoringServer) UpdateWorkloadRetryStatus(workloadType, name string, retryCount int, lastError string, nextRetry time.Duration) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	// Create nested maps if needed
	if m.healthStatus.WorkloadStatus[workloadType] == nil {
		m.healthStatus.WorkloadStatus[workloadType] = make(map[string]interface{})
	}

	workloadMap := m.healthStatus.WorkloadStatus[workloadType].(map[string]interface{})
	workloadMap[name] = map[string]interface{}{
		"active":     false,
		"healthy":    false,
		"retryCount": retryCount,
		"lastError":  lastError,
		"nextRetry":  time.Now().Add(nextRetry).Format(time.RFC3339),
	}
}

func (m *MonitoringServer) SetControllerState(state string) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	// Assuming we've enhanced the HealthStatus struct with a ControllerState field
	m.healthStatus.ControllerState = state
}

func (m *MonitoringServer) UpdateDBFailureMetrics(failureCount int, failureDuration time.Duration, threshold time.Duration, lastFailureTime, lastSuccessTime time.Time) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()

	// Assuming we've enhanced the HealthStatus struct with these fields
	m.healthStatus.DBFailureCount = failureCount
	m.healthStatus.DBFailureDuration = failureDuration
	m.healthStatus.DBFailureThreshold = threshold
	m.healthStatus.DBLastFailureTime = lastFailureTime
	m.healthStatus.DBLastSuccessTime = lastSuccessTime

	// If this controller has engaged split-brain protection (given up leadership due to DB issues)
	if failureDuration >= threshold {
		m.healthStatus.SplitBrainProtection = true
	}
}

// UpdateDBFailureMetrics updates the DB failure metrics
func (m *ControllerMetrics) UpdateDBFailureMetrics(
	failureCount int,
	failureDuration time.Duration,
	lastSuccessTime time.Time,
	state string,
	splitBrainProtectionActive bool) {

	m.DBConsecutiveFailures.Set(float64(failureCount))
	m.DBFailureDuration.Set(failureDuration.Seconds())
	m.DBLastSuccessTimestamp.Set(float64(lastSuccessTime.Unix()))

	// Update controller state gauge
	m.ControllerStateStatus.Reset()
	states := []string{"NORMAL", "DEGRADED_DB", "SPLIT_BRAIN_RISK"}
	for _, s := range states {
		if s == state {
			m.ControllerStateStatus.WithLabelValues(s).Set(1)
		} else {
			m.ControllerStateStatus.WithLabelValues(s).Set(0)
		}
	}

	// Update split-brain protection gauge
	if splitBrainProtectionActive {
		m.SplitBrainProtection.Set(1)
	} else {
		m.SplitBrainProtection.Set(0)
	}
}
