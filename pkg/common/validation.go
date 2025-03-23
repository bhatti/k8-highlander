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

// Package common provides configuration validation for k8-highlander.
//
// This file implements validation logic for all configuration types in the system.
// It ensures that configurations are complete, consistent, and valid before they
// are used to create workloads or initialize system components. The validation
// system provides detailed error messages that help users identify and fix
// configuration issues.
//
// Key features:
// - Field-level validation with specific error messages
// - Default value assignment for optional fields
// - Cross-field validation for related settings
// - Hierarchical validation for nested configurations
// - Consistent error reporting format
//
// Validation is a critical part of the system's reliability and security,
// preventing misconfigured workloads from being created and ensuring
// proper system initialization.

package common

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ValidationError represents a validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

// Validate validates a process configuration
func (cfg *ProcessConfig) Validate(defNS string) []ValidationError {
	if cfg.TerminationGracePeriod.Seconds() <= 0 {
		cfg.TerminationGracePeriod = time.Second * 30
	}
	if cfg.MaxRestarts <= 0 {
		cfg.MaxRestarts = 5
	}
	var errors []ValidationError

	if cfg.Namespace == "" {
		if defNS == "" {
			cfg.Namespace = "default"
		} else {
			cfg.Namespace = defNS
		}
	}
	// Validate required fields
	if cfg.Name == "" {
		errors = append(errors, ValidationError{
			Field:   "name",
			Message: "name is required",
		})
	}

	if cfg.Image == "" {
		errors = append(errors, ValidationError{
			Field:   "image",
			Message: "image is required",
		})
	}

	// Validate script
	if cfg.Script == nil {
		errors = append(errors, ValidationError{
			Field:   "script",
			Message: "script is required",
		})
	} else if len(cfg.Script.Commands) == 0 {
		errors = append(errors, ValidationError{
			Field:   "script.commands",
			Message: "at least one command is required",
		})
	}

	return errors
}

// Validate validates a cron job configuration
func (cfg *CronJobConfig) Validate(defNS string) []ValidationError {
	if cfg.TerminationGracePeriod.Seconds() <= 0 {
		cfg.TerminationGracePeriod = time.Second * 30
	}
	var errors []ValidationError

	// Validate required fields
	if cfg.Name == "" {
		errors = append(errors, ValidationError{
			Field:   "name",
			Message: "name is required",
		})
	}
	if cfg.Namespace == "" {
		if defNS == "" {
			cfg.Namespace = "default"
		} else {
			cfg.Namespace = defNS
		}
	}

	if cfg.Image == "" {
		errors = append(errors, ValidationError{
			Field:   "image",
			Message: "image is required",
		})
	}

	if cfg.Schedule == "" {
		errors = append(errors, ValidationError{
			Field:   "schedule",
			Message: "schedule is required",
		})
	}

	// Validate script
	if cfg.Script == nil {
		errors = append(errors, ValidationError{
			Field:   "script",
			Message: "script is required",
		})
	} else if len(cfg.Script.Commands) == 0 {
		errors = append(errors, ValidationError{
			Field:   "script.commands",
			Message: "at least one command is required",
		})
	}

	return errors
}

// Validate validates a service deployment configuration
func (cfg *ServiceConfig) Validate(defNS string) []ValidationError {
	if cfg.TerminationGracePeriod.Seconds() <= 0 {
		cfg.TerminationGracePeriod = time.Second * 30
	}
	var errors []ValidationError

	// Validate required fields
	if cfg.Name == "" {
		errors = append(errors, ValidationError{
			Field:   "name",
			Message: "name is required",
		})
	}
	if cfg.Namespace == "" {
		if defNS == "" {
			cfg.Namespace = "default"
		} else {
			cfg.Namespace = defNS
		}
	}

	if cfg.Image == "" {
		errors = append(errors, ValidationError{
			Field:   "image",
			Message: "image is required",
		})
	}

	if cfg.Replicas <= 0 {
		errors = append(errors, ValidationError{
			Field:   "replicas",
			Message: "replicas must be greater than 0",
		})
	}

	// Validate script
	if cfg.Script == nil {
		errors = append(errors, ValidationError{
			Field:   "script",
			Message: "script is required",
		})
	} else if len(cfg.Script.Commands) == 0 {
		errors = append(errors, ValidationError{
			Field:   "script.commands",
			Message: "at least one command is required",
		})
	}

	return errors
}

// Validate validates a stateful set configuration
func (cfg *PersistentConfig) Validate(defNS string) []ValidationError {
	if cfg.TerminationGracePeriod.Seconds() <= 0 {
		cfg.TerminationGracePeriod = time.Second * 30
	}
	var errors []ValidationError

	// Validate required fields
	if cfg.Name == "" {
		errors = append(errors, ValidationError{
			Field:   "name",
			Message: "name is required",
		})
	}
	if cfg.Namespace == "" {
		if defNS == "" {
			cfg.Namespace = "default"
		} else {
			cfg.Namespace = defNS
		}
	}

	if cfg.Image == "" {
		errors = append(errors, ValidationError{
			Field:   "image",
			Message: "image is required",
		})
	}

	if cfg.Replicas <= 0 {
		errors = append(errors, ValidationError{
			Field:   "replicas",
			Message: "replicas must be greater than 0",
		})
	}

	// Validate script
	if cfg.Script == nil {
		errors = append(errors, ValidationError{
			Field:   "script",
			Message: "script is required",
		})
	} else if len(cfg.Script.Commands) == 0 {
		errors = append(errors, ValidationError{
			Field:   "script.commands",
			Message: "at least one command is required",
		})
	}

	// Validate persistent volumes
	for i, volume := range cfg.PersistentVolumes {
		if volume.Name == "" {
			errors = append(errors, ValidationError{
				Field:   fmt.Sprintf("persistentVolumes[%d].name", i),
				Message: "name is required",
			})
		}

		if volume.MountPath == "" {
			errors = append(errors, ValidationError{
				Field:   fmt.Sprintf("persistentVolumes[%d].mountPath", i),
				Message: "mountPath is required",
			})
		}

		if volume.Size == "" {
			errors = append(errors, ValidationError{
				Field:   fmt.Sprintf("persistentVolumes[%d].size", i),
				Message: "size is required",
			})
		}
	}

	return errors
}

// Validate validates an application configuration.
func (cfg *AppConfig) Validate() error {
	// Set defaults
	if cfg.Port == 0 {
		cfg.Port = 8080
	}
	if cfg.ID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return err
		}
		cfg.ID = hostname
	}
	if cfg.Namespace == "" {
		cfg.Namespace = "default"
	}
	// Validate storage type
	if cfg.StorageType != "" && cfg.StorageType != StorageTypeRedis && cfg.StorageType != StorageTypeDB {
		return fmt.Errorf("invalid storage type: %s", cfg.StorageType)
	}

	// If using Redis, validate Redis configuration
	if cfg.StorageType == StorageTypeRedis || cfg.StorageType == "" {
		if cfg.Redis.Addr == "" {
			return fmt.Errorf("redis address is required when using Redis storage")
		}
	}

	// If using DB, validate database configuration
	if cfg.StorageType == StorageTypeDB {
		if cfg.DatabaseURL == "" {
			return fmt.Errorf("database URL is required when using DB storage")
		}
	}
	if cfg.Cluster.Name == "" {
		return fmt.Errorf("cluster name is not specified")
	}
	if cfg.Cluster.Kubeconfig == "" {
		return fmt.Errorf("cluster kubeconfig is not specified")
	}

	if len(cfg.Workloads.Processes) == 0 && len(cfg.Workloads.CronJobs) == 0 &&
		len(cfg.Workloads.Services) == 0 && len(cfg.Workloads.PersistentSets) == 0 {
		return fmt.Errorf("workload is not specified")
	}
	var allErrors []ValidationError
	for _, work := range cfg.Workloads.Processes {
		errors := work.Validate(cfg.Namespace)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}
	for _, work := range cfg.Workloads.CronJobs {
		errors := work.Validate(cfg.Namespace)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}
	for _, work := range cfg.Workloads.PersistentSets {
		errors := work.Validate(cfg.Namespace)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}
	for _, work := range cfg.Workloads.Services {
		errors := work.Validate(cfg.Namespace)
		if len(errors) > 0 {
			allErrors = append(allErrors, errors...)
		}
	}

	return ValidationErrors(allErrors)
}

func ValidationErrors(allErrors []ValidationError) error {
	if len(allErrors) > 0 {
		errorMessages := make([]string, len(allErrors))
		for i, err := range allErrors {
			errorMessages[i] = err.Error()
		}
		return fmt.Errorf("validation errors: %s", strings.Join(errorMessages, "; "))
	}
	return nil
}
