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

// Package process provides management for process-based workloads in k8-highlander.
//
// This file implements the ProcessManager, which coordinates multiple Process
// workloads as a cohesive group. It handles the lifecycle of Kubernetes Pods
// used for single-instance processes, providing unified management, configuration
// loading, and status reporting.
//
// Key features:
// - Dynamic loading of Process configurations from YAML files
// - Programmatic addition and removal of Process workloads
// - Coordinated starting and stopping of all managed Processes
// - Aggregated status reporting that reflects the overall health
// - Thread-safe operations for concurrent access
//
// The ProcessManager serves as the bridge between the workload manager and
// individual Process workloads, ensuring consistent management across all processes
// and proper integration with the leader election system. This ensures that only
// one controller instance manages these singleton processes across the entire cluster.

package process

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
)

// ProcessManager manages multiple process workloads as a single logical unit.
// It provides coordinated lifecycle management and status aggregation for all
// Pod-based processes under its control.
type ProcessManager struct {
	namespace        string
	processes        map[string]*ProcessWorkload
	processMutex     sync.RWMutex
	client           kubernetes.Interface
	configDir        string
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewProcessManager creates a new process manager
func NewProcessManager(namespace, configDir string, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) *ProcessManager {
	return &ProcessManager{
		namespace:        namespace,
		processes:        make(map[string]*ProcessWorkload),
		configDir:        configDir,
		metrics:          metrics,
		monitoringServer: monitoringServer,
	}
}

// Start initializes the manager and starts all registered Process workloads.
// It first loads configurations from files if a config directory was provided,
// then starts each workload with the given Kubernetes client.
func (m *ProcessManager) Start(ctx context.Context, client kubernetes.Interface) error {
	m.client = client

	// Load process configurations
	if err := m.loadProcessConfigs(); err != nil {
		return common.NewSevereError("ProcessManager", "failed to load process configs", err)
	}

	// Start all processes
	m.processMutex.RLock()
	defer m.processMutex.RUnlock()

	for _, process := range m.processes {
		if err := process.Start(ctx, client); err != nil {
			klog.Errorf("Failed to start process %s: %v", process.GetName(), err)
		}
	}

	return nil
}

// Stop gracefully terminates all managed Process workloads, ensuring proper
// cleanup of resources.
func (m *ProcessManager) Stop(ctx context.Context) error {
	m.processMutex.RLock()
	defer m.processMutex.RUnlock()

	for _, process := range m.processes {
		if err := process.Stop(ctx); err != nil {
			klog.Errorf("Failed to stop process %s: %v", process.GetName(), err)
		}
	}

	return nil
}

// GetStatus returns the status of the process manager
func (m *ProcessManager) GetStatus() common.WorkloadStatus {
	m.processMutex.RLock()
	defer m.processMutex.RUnlock()

	// Create a combined status
	status := common.WorkloadStatus{
		Name:    "processes",
		Type:    common.WorkloadTypeCustom,
		Active:  true,
		Healthy: true,
		Details: make(map[string]interface{}),
	}

	// Add process statuses
	processStatuses := make(map[string]common.WorkloadStatus)
	for name, process := range m.processes {
		processStatus := process.GetStatus()
		processStatuses[name] = processStatus

		// If any process is unhealthy, the manager is unhealthy
		if !processStatus.Healthy {
			status.Healthy = false
			status.LastError = fmt.Sprintf("Process %s is unhealthy: %s", name, processStatus.LastError)
		}
	}

	status.Details["processes"] = processStatuses

	return status
}

// GetName returns the name of the workload
func (m *ProcessManager) GetName() string {
	return "processes"
}

// GetType returns the type of the workload
func (m *ProcessManager) GetType() common.WorkloadType {
	return common.WorkloadTypeCustom
}

// loadProcessConfigs loads process configurations from files
func (m *ProcessManager) loadProcessConfigs() error {
	// Find all process config files
	yamlFiles, err := filepath.Glob(filepath.Join(m.configDir, "process-*.yaml"))
	if err != nil {
		return common.NewSevereError("ProcessManager", "failed to find process yaml config files", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(m.configDir, "process-*.yml"))
	if err != nil {
		return common.NewSevereError("ProcessManager", "failed to find process yml config files", err)
	}

	// Combine file lists
	configFiles := append(yamlFiles, ymlFiles...)

	// Load each config file
	for _, configFile := range configFiles {
		klog.Infof("Loading process config from %s", configFile)

		if err := m.loadProcessConfigFile(configFile); err != nil {
			klog.Errorf("Failed to load process config from %s: %v", configFile, err)
			return err
		}
	}

	return nil
}

// loadProcessConfigFile loads process configurations from a single file
func (m *ProcessManager) loadProcessConfigFile(configFile string) error {
	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return common.NewSevereError("ProcessManager", fmt.Sprintf("failed to read config file: %s", configFile), err)
	}

	klog.Infof("Read %d bytes from %s", len(data), configFile)
	//klog.Debug("File content: %s", string(data))

	// Parse the YAML
	var processConfigs []common.ProcessConfig
	if err := yaml.Unmarshal(data, &processConfigs); err != nil {
		// Try single process config
		var singleConfig common.ProcessConfig
		if err := yaml.Unmarshal(data, &singleConfig); err != nil {
			klog.Infof("Failed Config File: %s\n", data)
			return common.NewSevereError("ProcessManager", fmt.Sprintf("failed to parse process config file: %s", configFile), err)
		}
		processConfigs = []common.ProcessConfig{singleConfig}
	}

	klog.Infof("Parsed %d process configs from %s", len(processConfigs), configFile)

	// Create process workloads
	m.processMutex.Lock()
	defer m.processMutex.Unlock()

	for _, config := range processConfigs {
		// Set namespace if not specified
		if config.Namespace == "" {
			config.Namespace = m.namespace
		}

		// Create the process workload
		process, err := NewProcessWorkload(config, m.metrics, m.monitoringServer)
		if err != nil {
			return err
		}
		m.processes[config.Name] = process

		klog.Infof("Added process %s", config.Name)
	}

	return nil
}

// AddProcess adds a process to the manager
func (m *ProcessManager) AddProcess(config common.ProcessConfig) error {
	// Validate the configuration
	if err := config.Validate(""); err != nil {
		return common.NewSevereError("ProcessManager", "invalid process configuration", common.ValidationErrors(err))
	}
	m.processMutex.Lock()
	defer m.processMutex.Unlock()

	if _, exists := m.processes[config.Name]; exists {
		return fmt.Errorf("process %s already exists", config.Name)
	}

	// Set namespace if not specified
	if config.Namespace == "" {
		config.Namespace = m.namespace
	}

	// Create the process workload
	process, err := NewProcessWorkload(config, m.metrics, m.monitoringServer)
	if err != nil {
		return err
	}
	m.processes[config.Name] = process

	// Start the process if client is available
	if m.client != nil {
		if err := process.Start(context.Background(), m.client); err != nil {
			return fmt.Errorf("failed to start process: %w", err)
		}
	}

	return nil
}

// RemoveProcess removes a process from the manager
func (m *ProcessManager) RemoveProcess(name string) error {
	m.processMutex.Lock()
	defer m.processMutex.Unlock()

	process, exists := m.processes[name]
	if !exists {
		return fmt.Errorf("process %s does not exist", name)
	}

	// Stop the process if client is available
	if m.client != nil {
		if err := process.Stop(context.Background()); err != nil {
			return fmt.Errorf("failed to stop process: %w", err)
		}
	}

	delete(m.processes, name)
	return nil
}
