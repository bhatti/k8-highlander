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

// This file implements the manager for StatefulSet workloads in k8-highlander.
// It provides a higher-level abstraction that coordinates multiple StatefulSet
// workloads, handling their lifecycle as a group and providing unified status
// reporting and configuration management.
//
// The manager supports:
// - Dynamic loading of StatefulSet configurations from YAML files
// - Programmatic addition and removal of StatefulSet workloads
// - Coordinated starting and stopping of all managed StatefulSets
// - Aggregated status reporting for monitoring and dashboards
// - Thread-safe operations for concurrent access
//
// This implementation completes the persistent workload module by providing
// the management layer that integrates with the overall k8-highlander controller,
// allowing it to handle multiple StatefulSets as a cohesive workload type.

package persistent

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// var _ workloads.WorkloadManager = &PersistentManager{}

// PersistentManager manages multiple StatefulSet workloads as a single logical unit.
// It provides coordinated lifecycle management and status aggregation for all
// StatefulSets under its control.
type PersistentManager struct {
	namespace          string
	persistentSets     map[string]*PersistentWorkload
	persistentSetMutex sync.RWMutex
	client             kubernetes.Interface
	configDir          string
	status             common.WorkloadStatus
	metrics            *monitoring.ControllerMetrics
	monitoringServer   *monitoring.MonitoringServer
	statusMutex        sync.RWMutex
}

// NewPersistentManager creates a new persistent set manager
func NewPersistentManager(namespace, configDir string, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer, client kubernetes.Interface) *PersistentManager {
	return &PersistentManager{
		namespace:        namespace,
		persistentSets:   make(map[string]*PersistentWorkload),
		configDir:        configDir,
		metrics:          metrics,
		monitoringServer: monitoringServer,
		client:           client,
		status: common.WorkloadStatus{
			Name:    "persistent",
			Type:    common.WorkloadTypePersistentManager,
			Active:  false,
			Healthy: true,
			Details: make(map[string]interface{}),
		},
	}
}

// Start starts the persistent set manager
func (m *PersistentManager) Start(ctx context.Context) error {
	// Load persistent set configurations
	if err := m.loadStatefulSetConfigs(); err != nil {
		return common.NewSevereError("PersistentManager", "failed to load persistent set configs", err)
	}

	// Start all persistent sets
	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()

	for _, persistentSet := range m.persistentSets {
		if err := persistentSet.Start(ctx); err != nil {
			klog.Errorf("Failed to start persistent set %s: %v", persistentSet.GetName(), err)
		}
	}

	// Update status
	m.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = true
		s.LastTransition = time.Now()
	})

	return nil
}

// Stop stops the persistent set manager
func (m *PersistentManager) Stop(ctx context.Context) error {
	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()

	for _, persistentSet := range m.persistentSets {
		if err := persistentSet.Stop(ctx); err != nil {
			klog.Errorf("Failed to stop persistent set %s: %v", persistentSet.GetName(), err)
		}
	}

	// Update status
	m.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = false
		s.LastTransition = time.Now()
	})

	return nil
}

// GetStatus returns the status of the persistent set manager
func (m *PersistentManager) GetStatus() common.WorkloadStatus {
	m.statusMutex.RLock()
	defer m.statusMutex.RUnlock()

	// Create a copy of the status
	status := m.status

	// Add persistent set statuses
	persistentSetStatuses := make(map[string]common.WorkloadStatus)

	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()

	for name, persistentSet := range m.persistentSets {
		persistentSetStatuses[name] = persistentSet.GetStatus()
	}

	status.Details["persistent"] = persistentSetStatuses

	return status
}

// GetName returns the name of the workload
func (m *PersistentManager) GetName() string {
	return "persistent"
}

// GetType returns the type of the workload
func (m *PersistentManager) GetType() common.WorkloadType {
	return common.WorkloadTypePersistentManager
}

// loadStatefulSetConfigs loads persistent set configurations from files
func (m *PersistentManager) loadStatefulSetConfigs() error {
	// Find all persistent set config files
	yamlFiles, err := filepath.Glob(filepath.Join(m.configDir, "persistent-*.yaml"))
	if err != nil {
		return common.NewSevereError("PersistentManager", "failed to find persistent set yaml config files", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(m.configDir, "persistent-*.yml"))
	if err != nil {
		return common.NewSevereError("PersistentManager", "failed to find persistent set yml config files", err)
	}

	// Combine file lists
	configFiles := append(yamlFiles, ymlFiles...)

	// Load each config file
	for _, configFile := range configFiles {
		if err := m.loadStatefulSetConfigFile(configFile); err != nil {
			klog.Errorf("Failed to load persistent set config from %s: %v", configFile, err)
			return err
		}
	}

	return nil
}

// loadStatefulSetConfigFile loads persistent set configurations from a single file
func (m *PersistentManager) loadStatefulSetConfigFile(configFile string) error {
	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return common.NewSevereError("PersistentManager",
			fmt.Sprintf("failed to read config file: %s", configFile), err)
	}

	// Parse the YAML
	var persistentSetConfigs []common.PersistentConfig
	if err := yaml.Unmarshal(data, &persistentSetConfigs); err != nil {
		// Try single persistent set config
		var singleConfig common.PersistentConfig
		if err := yaml.Unmarshal(data, &singleConfig); err != nil {
			klog.Infof("Failed Config File: %s\n", data)
			return common.NewSevereError("PersistentManager",
				fmt.Sprintf("failed to parse persistent set config: %s", configFile), err)
		}
		persistentSetConfigs = []common.PersistentConfig{singleConfig}
	}

	// Create persistent set workloads
	m.persistentSetMutex.Lock()
	defer m.persistentSetMutex.Unlock()

	for _, config := range persistentSetConfigs {
		// Set namespace if not specified
		if config.Namespace == "" {
			config.Namespace = m.namespace
		}

		// Create the persistent set workload
		persistentSet, err := NewPersistentWorkload(config, m.metrics, m.monitoringServer, m.client)
		if err != nil {
			return err
		}
		m.persistentSets[config.Name] = persistentSet

		klog.Infof("Added persistent set %s", config.Name)
	}

	return nil
}

// AddWorkload adds a process to the manager
func (m *PersistentManager) AddWorkload(config any) error {
	if persistentConfig, ok := config.(common.PersistentConfig); ok {
		return m.AddStatefulSet(persistentConfig)
	}
	return common.NewSevereErrorMessage("PersistentManager", fmt.Sprintf(
		"invalid config type: %s", reflect.TypeOf(config).String()))
}

// AddStatefulSet adds a persistent set to the manager
func (m *PersistentManager) AddStatefulSet(config common.PersistentConfig) error {
	if err := config.Validate(""); err != nil {
		return common.NewSevereError("PersistentManager", "invalid persistent configuration", common.ValidationErrors(err))
	}
	m.persistentSetMutex.Lock()
	defer m.persistentSetMutex.Unlock()

	if _, exists := m.persistentSets[config.Name]; exists {
		return fmt.Errorf("persistent set %s already exists", config.Name)
	}

	// Set namespace if not specified
	if config.Namespace == "" {
		config.Namespace = m.namespace
	}

	// Create the persistent set workload
	persistentSet, err := NewPersistentWorkload(config, m.metrics, m.monitoringServer, m.client)
	if err != nil {
		return err
	}
	m.persistentSets[config.Name] = persistentSet

	// Start the persistent set if client is available
	if m.client != nil {
		if err := persistentSet.Start(context.Background()); err != nil {
			return fmt.Errorf("failed to start persistent set: %w", err)
		}
	}

	return nil
}

// RemoveWorkload removes a persistent set from the manager
func (m *PersistentManager) RemoveWorkload(name string) error {
	m.persistentSetMutex.Lock()
	defer m.persistentSetMutex.Unlock()

	persistentSet, exists := m.persistentSets[name]
	if !exists {
		return fmt.Errorf("persistent set %s does not exist", name)
	}

	// Stop the persistent set if client is available
	if m.client != nil {
		if err := persistentSet.Stop(context.Background()); err != nil {
			return fmt.Errorf("failed to stop persistent set: %w", err)
		}
	}

	delete(m.persistentSets, name)
	return nil
}

// GetWorkloadsWithCRD returns all workloads from the manager with CRD
func (m *PersistentManager) GetWorkloadsWithCRD() (res []common.Workload) {
	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()
	for _, w := range m.persistentSets {
		if w.config.WorkloadCRDRef != nil {
			res = append(res, w)
		}
	}
	return
}

// GetWorkload finds a persistent set from the manager
func (m *PersistentManager) GetWorkload(name string) (common.Workload, bool) {
	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()

	persistentSet, exists := m.persistentSets[name]
	if !exists {
		return nil, exists
	}

	return persistentSet, true
}

// GetWorkloadConfig finds a persistent set config from the manager
func (m *PersistentManager) GetWorkloadConfig(name string) (cfg common.BaseWorkloadConfig, ok bool) {
	m.persistentSetMutex.RLock()
	defer m.persistentSetMutex.RUnlock()

	persistentSet, exists := m.persistentSets[name]
	if !exists {
		return cfg, exists
	}

	return persistentSet.config.BaseWorkloadConfig, true
}

// GetConfig returns a workload info
func (m *PersistentManager) GetConfig() common.BaseWorkloadConfig {
	return common.BaseWorkloadConfig{}
}

// updateStatus updates the workload status
func (m *PersistentManager) updateStatus(updateFn func(*common.WorkloadStatus)) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()
	updateFn(&m.status)
}
