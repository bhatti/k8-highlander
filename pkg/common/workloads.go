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

// Package common provides core workload interfaces and types for k8-highlander.
//
// This file defines the central workload interface and related types that form
// the foundation of k8-highlander's workload management system. It establishes
// a common contract that all workload implementations must follow, enabling
// consistent handling of different workload types through a unified interface.
//
// Key components:
// - Workload interface definition
// - WorkloadType enumeration for different workload categories
// - WorkloadStatus structure for monitoring and reporting
// - BaseWorkloadConfig shared configuration foundation
// - Container building utilities for Kubernetes resources
//
// This architecture allows the system to manage diverse workload types
// (Processes, Services, CronJobs, StatefulSets) while maintaining a consistent
// management approach, monitoring system, and configuration model.

package common

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"strings"
	"time"
)

// WorkloadType represents the type of workload
type WorkloadType string

const (
	WorkloadTypeProcess           WorkloadType = "process"           // Generic process
	WorkloadTypeService           WorkloadType = "service"           // Kubernetes service deployment
	WorkloadTypeCronJob           WorkloadType = "cronjob"           // Kubernetes cron job
	WorkloadTypePersistent        WorkloadType = "persistent"        // Kubernetes stateful set
	WorkloadTypeProcessManager    WorkloadType = "processManager"    // Generic process manager
	WorkloadTypeServiceManager    WorkloadType = "serviceManager"    // Kubernetes service deployment manager
	WorkloadTypeCronJobManager    WorkloadType = "cronjobManager"    // Kubernetes cron job manager
	WorkloadTypePersistentManager WorkloadType = "persistentManager" // Kubernetes stateful set manager
	WorkloadTypeCustom            WorkloadType = "custom"            // Custom workload
)

// WorkloadStatus represents the current status of a workload
type WorkloadStatus struct {
	Name           string                 `json:"name"`
	Type           WorkloadType           `json:"type"`
	Active         bool                   `json:"active"`
	Healthy        bool                   `json:"healthy"`
	LastError      string                 `json:"lastError,omitempty"`
	LastTransition time.Time              `json:"lastTransition,omitempty"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

func (ws *WorkloadStatus) ActiveHealthyFloat() float64 {
	if ws.Healthy && ws.Active {
		return 1.0
	}
	return 0.0
}

// Workload defines the core interface that all workload types must implement.
// This interface provides the contract for lifecycle management and status reporting
// that enables the unified handling of different workload types.
type Workload interface {
	// Start starts the workload
	Start(ctx context.Context) error

	// Stop stops the workload
	Stop(ctx context.Context) error

	// GetStatus returns the current status of the workload
	GetStatus() WorkloadStatus

	// GetName returns the name of the workload
	GetName() string

	// GetType returns the type of the workload
	GetType() WorkloadType

	// GetConfig returns a workload info
	GetConfig() BaseWorkloadConfig
}

// BaseWorkloadConfig Common base configuration for all workload types
type BaseWorkloadConfig struct {
	Name          string            `json:"name" yaml:"name"`
	Namespace     string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Image         string            `json:"image" yaml:"image"`
	Script        *ScriptConfig     `json:"script,omitempty" yaml:"script,omitempty"`
	Command       []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Args          []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env           map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Resources     ResourceConfig    `json:"resources,omitempty" yaml:"resources,omitempty"`
	ConfigMaps    []string          `json:"configMaps,omitempty" yaml:"configMaps,omitempty"`
	Secrets       []string          `json:"secrets,omitempty" yaml:"secrets,omitempty"`
	Labels        map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
	Annotations   map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
	RestartPolicy string            `json:"restartPolicy,omitempty" yaml:"restartPolicy,omitempty"`
	NodeSelector  map[string]string `json:"nodeSelector,omitempty" yaml:"nodeSelector,omitempty"`
	//Tolerations   []Toleration      `json:"tolerations,omitempty" yaml:"tolerations,omitempty"`
	Ports                  []PortConfig          `json:"ports,omitempty" yaml:"ports,omitempty"`
	Sidecars               []ContainerConfig     `json:"sidecars,omitempty" yaml:"sidecars,omitempty"`
	Replicas               int32                 `json:"replicas"`
	TerminationGracePeriod time.Duration         `json:"terminationGracePeriod,omitempty" yaml:"terminationGracePeriod,omitempty"`
	WorkloadCRDRef         *WorkloadCRDReference `json:"workloadCRDRef,omitempty" yaml:"workloadCRDRef,omitempty"`
	ID                     string                `json:"-" yaml:"-"`
}

// WorkloadCRDReference defines a reference to a Workload CRD
type WorkloadCRDReference struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
	Name       string `json:"name" yaml:"name"`
	Namespace  string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
}

func ContainersSummary(containers []corev1.Container) string {
	var buf strings.Builder
	buf.WriteString(fmt.Sprintf("{container: %d, details:[", len(containers)))
	for i, c := range containers {
		if i > 0 {
			buf.WriteString(", ")
		}
		buf.WriteString(fmt.Sprintf("{name: %s, requests: %v, limits: %v}",
			c.Name, c.Resources.Requests, c.Resources.Limits))
	}
	buf.WriteString("]}")
	return buf.String()
}

func (c *BaseWorkloadConfig) BuildContainers(leaderInfo LocalLeaderInfo) (labels map[string]string, containers []corev1.Container, err error) {
	if !leaderInfo.IsLeader {
		klog.V(4).Infof("should not be able to build containers if not leader %v", leaderInfo)
		return labels, containers, fmt.Errorf("cannot build containers if not leader %v", leaderInfo)
	}

	// Set up environment variables
	var envVars []corev1.EnvVar
	for name, value := range c.Env {
		envVars = append(envVars, corev1.EnvVar{
			Name:  name,
			Value: value,
		})
	}

	// Set up labels
	labels = make(map[string]string)
	for k, v := range c.Labels {
		labels[k] = v
	}
	labels["app"] = c.Name
	labels["managed-by"] = PACKAGE
	labels["is-leader"] = fmt.Sprintf("%v", leaderInfo.IsLeader)
	labels["controller-id"] = leaderInfo.LeaderID
	labels["cluster-name"] = leaderInfo.ClusterName

	// Determine command and args from script
	var command []string
	var args []string

	if c.Script != nil && len(c.Script.Commands) > 0 {
		command, args = c.Script.BuildScriptCommand()
	} else {
		// Default to a simple shell command if no script is provided
		command = []string{"/bin/sh"}
		args = []string{"-c", "echo 'No script provided'"}
	}

	// Create the main container
	mainContainer := corev1.Container{
		Name:            "main",
		Image:           c.Image,
		Command:         command,
		Args:            args,
		Env:             envVars,
		Resources:       c.Resources.BuildResourceRequirements(),
		ImagePullPolicy: corev1.PullIfNotPresent,
	}
	klog.V(4).Infof("Main %s resource requirements: %+v (input %v)", c.Name, mainContainer.Resources, c.Resources)

	// Set up container ports
	var containerPorts []corev1.ContainerPort
	for _, portConfig := range c.Ports {
		containerPorts = append(containerPorts, corev1.ContainerPort{
			Name:          portConfig.Name,
			ContainerPort: portConfig.ContainerPort,
		})
	}
	mainContainer.Ports = containerPorts

	containers = append(containers, mainContainer)
	return labels, append(containers, c.BuildSidecars()...), nil
}

func (c *BaseWorkloadConfig) BuildSidecars() (sidecars []corev1.Container) {
	for _, sidecar := range c.Sidecars {
		// Set up environment variables for sidecar
		var sidecarEnvVars []corev1.EnvVar
		for name, value := range sidecar.Env {
			sidecarEnvVars = append(sidecarEnvVars, corev1.EnvVar{
				Name:  name,
				Value: value,
			})
		}

		// Create sidecar container
		sidecarContainer := corev1.Container{
			Name:            sidecar.Name,
			Image:           sidecar.Image,
			Command:         sidecar.Command,
			Args:            sidecar.Args,
			Env:             sidecarEnvVars,
			Resources:       sidecar.Resources.BuildResourceRequirements(),
			ImagePullPolicy: corev1.PullIfNotPresent,
		}
		klog.V(4).Infof("Sidecar %s resource requirements: %+v (input %v)", c.Name, sidecarContainer.Resources, sidecar.Resources)

		// Add volume mounts
		for _, volumeMount := range sidecar.VolumeMounts {
			sidecarContainer.VolumeMounts = append(sidecarContainer.VolumeMounts, corev1.VolumeMount{
				Name:      volumeMount.Name,
				MountPath: volumeMount.MountPath,
				ReadOnly:  volumeMount.ReadOnly,
			})
		}

		// Add security context if specified
		if sidecar.SecurityContext != nil {
			sidecarContainer.SecurityContext = &corev1.SecurityContext{
				Privileged:             sidecar.SecurityContext.Privileged,
				RunAsUser:              sidecar.SecurityContext.RunAsUser,
				RunAsGroup:             sidecar.SecurityContext.RunAsGroup,
				RunAsNonRoot:           sidecar.SecurityContext.RunAsNonRoot,
				ReadOnlyRootFilesystem: sidecar.SecurityContext.ReadOnlyRootFilesystem,
			}
		}

		// Add the sidecar container to the pod
		sidecars = append(sidecars, sidecarContainer)
	}
	return
}
