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

// Package service provides management for service-based workloads in k8-highlander.
//
// This file implements the DeploymentManager, which coordinates multiple Service
// workloads as a cohesive group. It handles the lifecycle of Kubernetes Deployments
// used for long-running services, providing unified management, configuration loading,
// and status reporting.
//
// Key features:
// - Dynamic loading of Deployment configurations from YAML files
// - Programmatic addition and removal of Service workloads
// - Coordinated starting and stopping of all managed Deployments
// - Aggregated status reporting for monitoring and dashboards
// - Thread-safe operations for concurrent access
//
// The DeploymentManager serves as the bridge between the workload manager and
// individual Service workloads, ensuring consistent management across all services
// and proper integration with the leader election system.

package service

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"gopkg.in/yaml.v3"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DeploymentManager manages multiple deployment workloads as a single logical unit.
// It provides coordinated lifecycle management and status aggregation for all
// Deployment-based services under its control.
type DeploymentManager struct {
	namespace        string
	deployments      map[string]*ServiceWorkload
	deploymentMutex  sync.RWMutex
	client           kubernetes.Interface
	configDir        string
	status           common.WorkloadStatus
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
	statusMutex      sync.RWMutex
}

// NewDeploymentManager creates a new deployment manager
func NewDeploymentManager(namespace, configDir string, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) *DeploymentManager {
	return &DeploymentManager{
		namespace:        namespace,
		deployments:      make(map[string]*ServiceWorkload),
		configDir:        configDir,
		metrics:          metrics,
		monitoringServer: monitoringServer,
		status: common.WorkloadStatus{
			Name:    "deployments",
			Type:    common.WorkloadTypeCustom,
			Active:  false,
			Healthy: true,
			Details: make(map[string]interface{}),
		},
	}
}

// Start initializes the manager and starts all registered Deployment workloads.
// It first loads configurations from files if a config directory was provided,
// then starts each workload with the given Kubernetes client.
func (m *DeploymentManager) Start(ctx context.Context, client kubernetes.Interface) error {
	m.client = client

	// Load deployment configurations
	if err := m.loadDeploymentConfigs(); err != nil {
		return common.NewSevereError("DeploymentManager", "failed to load deployment configs", err)
	}

	// Start all deployments
	m.deploymentMutex.RLock()
	defer m.deploymentMutex.RUnlock()

	for _, deployment := range m.deployments {
		if err := deployment.Start(ctx, client); err != nil {
			klog.Errorf("Failed to start deployment %s: %v", deployment.GetName(), err)
		}
	}

	// Update status
	m.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = true
		s.LastTransition = time.Now()
	})

	return nil
}

// Stop gracefully terminates all managed Deployment workloads, ensuring proper
// cleanup of resources.
func (m *DeploymentManager) Stop(ctx context.Context) error {
	m.deploymentMutex.RLock()
	defer m.deploymentMutex.RUnlock()

	for _, deployment := range m.deployments {
		if err := deployment.Stop(ctx); err != nil {
			klog.Errorf("Failed to stop deployment %s: %v", deployment.GetName(), err)
		}
	}

	// Update status
	m.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = false
		s.LastTransition = time.Now()
	})

	return nil
}

// GetStatus returns the status of the deployment manager
func (m *DeploymentManager) GetStatus() common.WorkloadStatus {
	m.statusMutex.RLock()
	defer m.statusMutex.RUnlock()

	// Create a copy of the status
	status := m.status

	// Add deployment statuses
	deploymentStatuses := make(map[string]common.WorkloadStatus)

	m.deploymentMutex.RLock()
	defer m.deploymentMutex.RUnlock()

	for name, deployment := range m.deployments {
		deploymentStatuses[name] = deployment.GetStatus()
	}

	status.Details["deployments"] = deploymentStatuses

	return status
}

// GetName returns the name of the workload
func (m *DeploymentManager) GetName() string {
	return "deployments"
}

// GetType returns the type of the workload
func (m *DeploymentManager) GetType() common.WorkloadType {
	return common.WorkloadTypeCustom
}

// loadDeploymentConfigs loads deployment configurations from files
func (m *DeploymentManager) loadDeploymentConfigs() error {
	// Find all deployment config files
	yamlFiles, err := filepath.Glob(filepath.Join(m.configDir, "deployment-*.yaml"))
	if err != nil {
		return common.NewSevereError("DeploymentManager", "failed to find deployment yaml config files", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(m.configDir, "deployment-*.yml"))
	if err != nil {
		return common.NewSevereError("DeploymentManager", "failed to find deployment yml config files", err)
	}

	// Combine file lists
	configFiles := append(yamlFiles, ymlFiles...)

	// Load each config file
	for _, configFile := range configFiles {
		if err := m.loadDeploymentConfigFile(configFile); err != nil {
			klog.Errorf("Failed to load deployment config from %s: %v", configFile, err)
			continue
		}
	}

	return nil
}

// loadDeploymentConfigFile loads deployment configurations from a single file
func (m *DeploymentManager) loadDeploymentConfigFile(configFile string) error {
	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return common.NewSevereError("DeploymentManager", fmt.Sprintf("failed to read config file: %s", configFile), err)
	}

	// Parse the YAML
	var deploymentConfigs []common.ServiceConfig
	if err := yaml.Unmarshal(data, &deploymentConfigs); err != nil {
		// Try single deployment config
		var singleConfig common.ServiceConfig
		if err := yaml.Unmarshal(data, &singleConfig); err != nil {
			klog.Infof("Failed Config File: %s\n", data)
			return common.NewSevereError("DeploymentManager", fmt.Sprintf("failed to parse deployment config file: %s", configFile), err)
		}
		deploymentConfigs = []common.ServiceConfig{singleConfig}
	}

	// Create deployment workloads
	m.deploymentMutex.Lock()
	defer m.deploymentMutex.Unlock()

	for _, config := range deploymentConfigs {
		// Set namespace if not specified
		if config.Namespace == "" {
			config.Namespace = m.namespace
		}

		// Create the deployment workload
		deployment, err := NewServiceWorkload(config, m.metrics, m.monitoringServer)
		if err != nil {
			return err
		}
		m.deployments[config.Name] = deployment

		klog.Infof("Added deployment %s", config.Name)
	}

	return nil
}

// AddDeployment adds a deployment to the manager
func (m *DeploymentManager) AddDeployment(config common.ServiceConfig) error {
	if err := config.Validate(""); err != nil {
		return common.NewSevereError("DeploymentManager", "invalid deployment configuration", common.ValidationErrors(err))
	}
	m.deploymentMutex.Lock()
	defer m.deploymentMutex.Unlock()

	if _, exists := m.deployments[config.Name]; exists {
		return fmt.Errorf("deployment %s already exists", config.Name)
	}

	// Set namespace if not specified
	if config.Namespace == "" {
		config.Namespace = m.namespace
	}

	// Create the deployment workload
	deployment, err := NewServiceWorkload(config, m.metrics, m.monitoringServer)
	if err != nil {
		return err
	}
	m.deployments[config.Name] = deployment

	// Start the deployment if client is available
	if m.client != nil {
		if err := deployment.Start(context.Background(), m.client); err != nil {
			return fmt.Errorf("failed to start deployment: %w", err)
		}
	}

	return nil
}

// RemoveDeployment removes a deployment from the manager
func (m *DeploymentManager) RemoveDeployment(name string) error {
	m.deploymentMutex.Lock()
	defer m.deploymentMutex.Unlock()

	deployment, exists := m.deployments[name]
	if !exists {
		return fmt.Errorf("deployment %s does not exist", name)
	}

	// Stop the deployment if client is available
	if m.client != nil {
		if err := deployment.Stop(context.Background()); err != nil {
			return fmt.Errorf("failed to stop deployment: %w", err)
		}
	}

	delete(m.deployments, name)
	return nil
}

// updateStatus updates the workload status
func (m *DeploymentManager) updateStatus(updateFn func(*common.WorkloadStatus)) {
	m.statusMutex.Lock()
	defer m.statusMutex.Unlock()
	updateFn(&m.status)
}
