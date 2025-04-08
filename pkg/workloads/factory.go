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

// Package workloads provides the factory and manager for k8-highlander workloads.
//
// This file implements a factory pattern for creating different types of workloads
// from configuration files or programmatic definitions. It serves as a bridge between
// configuration and the concrete workload implementations, providing a unified way
// to instantiate and initialize workloads regardless of their specific type.
//
// The factory supports:
// - Loading workload configurations from YAML, YML, and JSON files
// - Creating appropriate workload instances based on type
// - Handling both single and multi-workload configuration files
// - Automatic registration of workloads with the workload manager
// - Error handling and validation during workload creation
//
// Supported workload types:
// - Process: Single-instance process workloads (pods)
// - Service: Deployments with services for network access
// - CronJob: Scheduled recurring tasks
// - Persistent: StatefulSets with persistent storage
//
// This factory design enables flexible configuration options while enforcing
// consistency and validation across all workload types.

package workloads

import (
	"encoding/json"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"gopkg.in/yaml.v3"
	"k8s.io/klog/v2"
	"os"
	"path/filepath"

	"github.com/bhatti/k8-highlander/pkg/workloads/cronjob"
	"github.com/bhatti/k8-highlander/pkg/workloads/persistent"
	"github.com/bhatti/k8-highlander/pkg/workloads/process"
	"github.com/bhatti/k8-highlander/pkg/workloads/service"
)

// WorkloadFactory creates workloads from configuration files or programmatic definitions.
// It handles the complexity of parsing different configuration formats and instantiating
// the appropriate workload implementations.
type WorkloadFactory struct {
	configDir        string
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewWorkloadFactory creates a new workload factory
func NewWorkloadFactory(configDir string, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) *WorkloadFactory {
	return &WorkloadFactory{
		configDir:        configDir,
		metrics:          metrics,
		monitoringServer: monitoringServer,
	}
}

// WorkloadConfig represents a generic workload configuration that can be used
// to create any type of workload. It contains the common fields needed for
// all workloads and a type-specific configuration map.
type WorkloadConfig struct {
	Type   common.WorkloadType    `json:"type"`
	Name   string                 `json:"name"`
	Config map[string]interface{} `json:"config"`
}

// LoadWorkloadsFromConfig scans the configuration directory for workload definition
// files and loads them into the provided workload manager. It supports YAML, YML,
// and JSON file formats.
func (f *WorkloadFactory) LoadWorkloadsFromConfig(manager Manager) error {
	// Find all config files in the config directory
	yamlFiles, err := filepath.Glob(filepath.Join(f.configDir, "*.yaml"))
	if err != nil {
		// THIS SHOULD BE SEVERE
		return fmt.Errorf("failed to find yaml config files: %w", err)
	}

	ymlFiles, err := filepath.Glob(filepath.Join(f.configDir, "*.yml"))
	if err != nil {
		return fmt.Errorf("failed to find yml config files: %w", err)
	}

	jsonFiles, err := filepath.Glob(filepath.Join(f.configDir, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to find json config files: %w", err)
	}

	// Combine all file lists
	configFiles := append(yamlFiles, ymlFiles...)
	configFiles = append(configFiles, jsonFiles...)

	// Load each config file
	for _, configFile := range configFiles {
		if err := f.loadWorkloadFile(configFile, manager); err != nil {
			klog.Errorf("Failed to load workload from %s: %v", configFile, err)
			continue
		}
	}

	return nil
}

// loadWorkloadFile loads workloads from a single configuration file
func (f *WorkloadFactory) loadWorkloadFile(configFile string, manager Manager) error {
	// Read the file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return common.NewSevereError("WorkloadFactory", fmt.Sprintf("failed to read config file: %s", configFile), err)
	}

	// Determine file type
	var workloadConfigs []WorkloadConfig
	ext := filepath.Ext(configFile)

	if ext == ".json" {
		// Parse JSON
		if err := json.Unmarshal(data, &workloadConfigs); err != nil {
			// Try single workload
			var singleConfig WorkloadConfig
			if err := json.Unmarshal(data, &singleConfig); err != nil {
				return common.NewSevereError("WorkloadFactory",
					fmt.Sprintf("failed to read config file: %s", configFile), err)
			}
			workloadConfigs = []WorkloadConfig{singleConfig}
		}
	} else {
		// Parse YAML
		if err := yaml.Unmarshal(data, &workloadConfigs); err != nil {
			// Try single workload
			var singleConfig WorkloadConfig
			if err := yaml.Unmarshal(data, &singleConfig); err != nil {
				klog.Infof("Failed Config File: %s\n", data)
				return common.NewSevereError("WorkloadFactory",
					fmt.Sprintf("failed to parse YAML config file: %s", configFile), err)
			}
			workloadConfigs = []WorkloadConfig{singleConfig}
		}
	}

	// Create workloads
	for _, config := range workloadConfigs {
		workload, err := f.CreateWorkload(config)
		if err != nil {
			klog.Errorf("Failed to create workload %s: %v", config.Name, err)
			continue
		}

		if err := manager.AddWorkload(workload); err != nil {
			klog.Errorf("Failed to add workload %s: %v", config.Name, err)
			continue
		}

		klog.Infof("Added workload %s of type %s", config.Name, config.Type)
	}

	return nil
}

// CreateWorkload instantiates a specific workload based on its type and configuration.
// It handles the conversion of generic configuration to type-specific structures
// and delegates to the appropriate workload constructors.
func (f *WorkloadFactory) CreateWorkload(config WorkloadConfig) (common.Workload, error) {
	// Convert config to appropriate type
	configBytes, err := json.Marshal(config.Config)
	if err != nil {
		return nil, common.NewSevereError("WorkloadFactory", "failed to marshal config", err)
	}

	switch config.Type {
	case common.WorkloadTypeProcess:
		var processConfig common.ProcessConfig
		if err := json.Unmarshal(configBytes, &processConfig); err != nil {
			return nil, common.NewSevereError("WorkloadFactory", "failed to unmarshal process config", err)
		}

		// Set name if not specified
		if processConfig.Name == "" {
			processConfig.Name = config.Name
		}

		return process.NewProcessWorkload(processConfig, f.metrics, f.monitoringServer)

	case common.WorkloadTypeService:
		var deploymentConfig common.ServiceConfig
		if err := json.Unmarshal(configBytes, &deploymentConfig); err != nil {
			return nil, common.NewSevereError("WorkloadFactory", "failed to unmarshal deployment config", err)
		}

		// Set name if not specified
		if deploymentConfig.Name == "" {
			deploymentConfig.Name = config.Name
		}

		return service.NewServiceWorkload(deploymentConfig, f.metrics, f.monitoringServer)

	case common.WorkloadTypeCronJob:
		var cronJobConfig common.CronJobConfig
		if err := json.Unmarshal(configBytes, &cronJobConfig); err != nil {
			return nil, common.NewSevereError("WorkloadFactory", "failed to unmarshal cron job config", err)
		}

		// Set name if not specified
		if cronJobConfig.Name == "" {
			cronJobConfig.Name = config.Name
		}

		return cronjob.NewCronJobWorkload(cronJobConfig, f.metrics, f.monitoringServer)

	case common.WorkloadTypePersistent:
		var statefulSetConfig common.PersistentConfig
		if err := json.Unmarshal(configBytes, &statefulSetConfig); err != nil {
			return nil, common.NewSevereError("WorkloadFactory", "failed to unmarshal persistent set config", err)
		}

		// Set name if not specified
		if statefulSetConfig.Name == "" {
			statefulSetConfig.Name = config.Name
		}

		return persistent.NewPersistentWorkload(statefulSetConfig, f.metrics, f.monitoringServer)

	default:
		return nil, common.NewSevereErrorMessage("WorkloadFactory", fmt.Sprintf("unsupported workload type: %s", config.Type))
	}
}
