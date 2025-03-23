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

// Package cronjob implements the CronJob workload type for k8-highlander.
//
// This file provides the manager that coordinates multiple CronJob workloads,
// enabling the controller to manage scheduled tasks in Kubernetes. The CronJob
// manager ensures that only one instance of the controller manages scheduled tasks,
// preventing duplicate job executions across cluster nodes.
//
// Key features:
// - Dynamic loading of CronJob configurations from YAML files
// - Programmatic addition and removal of CronJob workloads
// - Coordinated starting and stopping of all managed CronJobs
// - Aggregated status reporting for monitoring and dashboards
// - Thread-safe operations for concurrent access
//
// CronJobs are especially useful for:
// - Scheduled data processing or ETL tasks
// - Periodic cleanup operations
// - Report generation
// - Backup procedures
// - Any task that should run on a schedule but exactly once
//
// The manager implementation ensures all registered CronJobs remain
// under the control of a single leader instance, preventing duplicate
// job executions in multi-node deployments.

package cronjob

import (
	"context"
	"fmt"
	"gopkg.in/yaml.v3"
	"k8-highlander/pkg/common"
	"k8-highlander/pkg/monitoring"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CronJobManager manages multiple CronJob workloads as a single logical unit.
// It provides coordinated lifecycle management and status aggregation for all
// CronJobs under its control.
type CronJobManager struct {
	namespace        string
	cronJobs         map[string]*CronJobWorkload
	cronJobMutex     sync.RWMutex
	client           kubernetes.Interface
	configDir        string
	status           common.WorkloadStatus
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
	statusMutex      sync.RWMutex
}

// NewCronJobManager creates a new cron job manager
func NewCronJobManager(namespace, configDir string, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) *CronJobManager {
	return &CronJobManager{
		namespace:        namespace,
		cronJobs:         make(map[string]*CronJobWorkload),
		configDir:        configDir,
		metrics:          metrics,
		monitoringServer: monitoringServer,
		status: common.WorkloadStatus{
			Name:    "cronjobs",
			Type:    common.WorkloadTypeCustom,
			Active:  false,
			Healthy: true,
			Details: make(map[string]interface{}),
		},
	}
}

// Start initializes the manager and starts all registered CronJob workloads.
// It first loads configurations from files if a config directory was provided,
// then starts each workload with the given Kubernetes client.
func (cm *CronJobManager) Start(ctx context.Context, client kubernetes.Interface) error {
	cm.client = client

	// Load cron job configurations
	if err := cm.loadCronJobConfigs(); err != nil {
		return common.NewSevereError("CronJobManager", "failed to load cron job configs", err)
	}

	// Start all cron jobs
	cm.cronJobMutex.RLock()
	defer cm.cronJobMutex.RUnlock()

	for _, cronJob := range cm.cronJobs {
		if err := cronJob.Start(ctx, client); err != nil {
			klog.Errorf("Failed to start cron job %s: %v", cronJob.GetName(), err)
		}
	}

	// Update status
	cm.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = true
		s.LastTransition = time.Now()
	})

	return nil
}

// Stop gracefully terminates all managed CronJob workloads, ensuring proper
// cleanup of resources.
func (cm *CronJobManager) Stop(ctx context.Context) error {
	cm.cronJobMutex.RLock()
	defer cm.cronJobMutex.RUnlock()

	for _, cronJob := range cm.cronJobs {
		if err := cronJob.Stop(ctx); err != nil {
			klog.Errorf("Failed to stop cron job %s: %v", cronJob.GetName(), err)
		}
	}

	// Update status
	cm.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = false
		s.LastTransition = time.Now()
	})

	return nil
}

// GetStatus returns the status of the cron job manager
func (cm *CronJobManager) GetStatus() common.WorkloadStatus {
	cm.statusMutex.RLock()
	defer cm.statusMutex.RUnlock()

	// Create a copy of the status
	status := cm.status

	// Add cron job statuses
	cronJobStatuses := make(map[string]common.WorkloadStatus)

	cm.cronJobMutex.RLock()
	defer cm.cronJobMutex.RUnlock()

	for name, cronJob := range cm.cronJobs {
		cronJobStatuses[name] = cronJob.GetStatus()
	}

	status.Details["cronJobs"] = cronJobStatuses

	return status
}

// GetName returns the name of the workload
func (cm *CronJobManager) GetName() string {
	return "cronjobs"
}

// GetType returns the type of the workload
func (cm *CronJobManager) GetType() common.WorkloadType {
	return common.WorkloadTypeCustom
}

// loadCronJobConfigs loads cron job configurations from files
func (cm *CronJobManager) loadCronJobConfigs() error {
	// Find all cron job config files
	yamlFiles, err := filepath.Glob(filepath.Join(cm.configDir, "cronjob-*.yaml"))
	if err != nil {
		return common.NewSevereError("CronJobManager", "failed to find cron job yaml config files", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(cm.configDir, "cronjob-*.yml"))
	if err != nil {
		return common.NewSevereError("CronJobManager", "failed to find cron job yml config files", err)
	}

	// Combine file lists
	configFiles := append(yamlFiles, ymlFiles...)

	// Load each config file
	for _, configFile := range configFiles {
		if err := cm.loadCronJobConfigFile(configFile); err != nil {
			klog.Errorf("Failed to load cron job config from %s: %v", configFile, err)
			return err
		}
	}

	return nil
}

// loadCronJobConfigFile loads cron job configurations from a single file
func (cm *CronJobManager) loadCronJobConfigFile(configFile string) error {
	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return common.NewSevereError("CronJobManager", fmt.Sprintf("failed to read config file (%s)", configFile), err)
	}

	// Parse the YAML
	var cronJobConfigs []common.CronJobConfig
	if err := yaml.Unmarshal(data, &cronJobConfigs); err != nil {
		// Try single cron job config
		var singleConfig common.CronJobConfig
		if err := yaml.Unmarshal(data, &singleConfig); err != nil {
			klog.Infof("Failed Config File: %s\n", data)
			return common.NewSevereError("CronJobManager", fmt.Sprintf("failed to parse cron job config (%s)", configFile), err)
		}
		cronJobConfigs = []common.CronJobConfig{singleConfig}
	}

	// Create cron job workloads
	cm.cronJobMutex.Lock()
	defer cm.cronJobMutex.Unlock()

	for _, config := range cronJobConfigs {
		// Set namespace if not specified
		if config.Namespace == "" {
			config.Namespace = cm.namespace
		}

		// Create the cron job workload
		cronJob, err := NewCronJobWorkload(config, cm.metrics, cm.monitoringServer)
		if err != nil {
			return err
		}
		cm.cronJobs[config.Name] = cronJob

		klog.Infof("Added cron job %s", config.Name)
	}

	return nil
}

// AddCronJob adds a cron job to the manager
func (cm *CronJobManager) AddCronJob(config common.CronJobConfig) error {
	if err := config.Validate(""); err != nil {
		return common.NewSevereError("CronJobManager", "invalid cron configuration", common.ValidationErrors(err))
	}
	cm.cronJobMutex.Lock()
	defer cm.cronJobMutex.Unlock()

	if _, exists := cm.cronJobs[config.Name]; exists {
		return fmt.Errorf("cron job %s already exists", config.Name)
	}

	// Set namespace if not specified
	if config.Namespace == "" {
		config.Namespace = cm.namespace
	}

	// Create the cron job workload
	cronJob, err := NewCronJobWorkload(config, cm.metrics, cm.monitoringServer)
	if err != nil {
		return err
	}
	cm.cronJobs[config.Name] = cronJob

	// Start the cron job if client is available
	if cm.client != nil {
		if err := cronJob.Start(context.Background(), cm.client); err != nil {
			return fmt.Errorf("failed to start cron job: %w", err)
		}
	}

	return nil
}

// RemoveCronJob removes a cron job from the manager
func (cm *CronJobManager) RemoveCronJob(name string) error {
	cm.cronJobMutex.Lock()
	defer cm.cronJobMutex.Unlock()

	cronJob, exists := cm.cronJobs[name]
	if !exists {
		return fmt.Errorf("cron job %s does not exist", name)
	}

	// Stop the cron job if client is available
	if cm.client != nil {
		if err := cronJob.Stop(context.Background()); err != nil {
			return fmt.Errorf("failed to stop cron job: %w", err)
		}
	}

	delete(cm.cronJobs, name)
	return nil
}

// updateStatus updates the workload status
func (cm *CronJobManager) updateStatus(updateFn func(*common.WorkloadStatus)) {
	cm.statusMutex.Lock()
	defer cm.statusMutex.Unlock()
	updateFn(&cm.status)
}
