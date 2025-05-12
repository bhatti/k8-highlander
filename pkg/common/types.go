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

// Package common provides shared types and utilities for k8-highlander.
//
// This file implements the core shared types, structures, and helper functions
// used throughout the k8-highlander system. It defines common elements such as
// container configurations, resource specifications, volume settings, and error
// handling that are used by multiple workload types.
//
// Key components:
// - Error categorization with SevereError type
// - Container configuration structures
// - Resource requirement translations
// - Volume and mount definitions
// - Security context specifications
// - Helper functions for creating Kubernetes resources
//
// These shared components ensure consistency across different workload types
// and reduce code duplication by centralizing common functionality in a single
// package.

package common

import (
	"context"
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"time"
)

// SevereError represents a severe error initializing such as bad file/format
type SevereError struct {
	Component string
	Message   string
}

func NewSevereError(component string, msg string, err error) *SevereError {
	if err != nil {
		return &SevereError{Component: component, Message: fmt.Sprintf("%s: %s", msg, err)}
	}
	return &SevereError{Component: component, Message: msg}
}

func NewSevereErrorMessage(component string, msg string) *SevereError {
	return &SevereError{Component: component, Message: msg}
}

func (e *SevereError) Is(target error) bool {
	_, ok := target.(*SevereError)
	return ok
}

func (e SevereError) Error() string {
	return fmt.Sprintf("%s: %s", e.Component, e.Message)
}

// ContainerConfig defines configuration for a container
type ContainerConfig struct {
	Name            string            `json:"name" yaml:"name"`
	Image           string            `json:"image" yaml:"image"`
	Command         []string          `json:"command,omitempty" yaml:"command,omitempty"`
	Args            []string          `json:"args,omitempty" yaml:"args,omitempty"`
	Env             map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Resources       ResourceConfig    `json:"resources,omitempty" yaml:"resources,omitempty"`
	VolumeMounts    []VolumeMount     `json:"volumeMounts,omitempty" yaml:"volumeMounts,omitempty"`
	SecurityContext *SecurityContext  `json:"securityContext,omitempty" yaml:"securityContext,omitempty"`
}

// VolumeMount defines a volume mount
type VolumeMount struct {
	Name      string `json:"name" yaml:"name"`
	MountPath string `json:"mountPath" yaml:"mountPath"`
	ReadOnly  bool   `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}

// SecurityContext defines security context for a container
type SecurityContext struct {
	Privileged             *bool  `json:"privileged,omitempty" yaml:"privileged,omitempty"`
	RunAsUser              *int64 `json:"runAsUser,omitempty" yaml:"runAsUser,omitempty"`
	RunAsGroup             *int64 `json:"runAsGroup,omitempty" yaml:"runAsGroup,omitempty"`
	RunAsNonRoot           *bool  `json:"runAsNonRoot,omitempty" yaml:"runAsNonRoot,omitempty"`
	ReadOnlyRootFilesystem *bool  `json:"readOnlyRootFilesystem,omitempty" yaml:"readOnlyRootFilesystem,omitempty"`
}

// ResourceConfig defines resource requirements
type ResourceConfig struct {
	CPURequest    string `json:"cpuRequest,omitempty" yaml:"cpuRequest,omitempty"`
	MemoryRequest string `json:"memoryRequest,omitempty" yaml:"memoryRequest,omitempty"`
	CPULimit      string `json:"cpuLimit,omitempty" yaml:"cpuLimit,omitempty"`
	MemoryLimit   string `json:"memoryLimit,omitempty" yaml:"memoryLimit,omitempty"`
}

// VolumeConfig defines a persistent volume configuration
type VolumeConfig struct {
	Name             string `json:"name" yaml:"name"`
	MountPath        string `json:"mountPath" yaml:"mountPath"`
	StorageClassName string `json:"storageClassName,omitempty" yaml:"storageClassName,omitempty"`
	Size             string `json:"size" yaml:"size"`
}

// PortConfig defines a container port configuration
type PortConfig struct {
	Name          string `json:"name" yaml:"name"`
	ContainerPort int32  `json:"containerPort" yaml:"containerPort"`
	ServicePort   int32  `json:"servicePort,omitempty" yaml:"servicePort,omitempty"`
}

// ScriptConfig defines a script to run
type ScriptConfig struct {
	Commands []string `json:"commands" yaml:"commands"`
	Shell    string   `json:"shell,omitempty" yaml:"shell,omitempty"`
}

// BuildScriptCommand builds a command to run scripts
func (s *ScriptConfig) BuildScriptCommand() ([]string, []string) {
	// Handle nil script
	if s == nil {
		return []string{"/bin/sh"}, []string{"-c", "echo 'No script provided'; sleep infinity"}
	}
	shell := s.Shell
	if shell == "" {
		shell = "/bin/sh"
	}

	// Create a script that runs all commands
	scriptContent := "#!/bin/sh\nset -e\n\n"
	for _, cmd := range s.Commands {
		scriptContent += cmd + "\n"
	}

	// Return command to execute the script
	return []string{shell}, []string{"-c", scriptContent}
}

// BuildResourceRequirements converts ResourceConfig to Kubernetes ResourceRequirements
func (c ResourceConfig) BuildResourceRequirements() corev1.ResourceRequirements {
	resources := corev1.ResourceRequirements{}

	if c.CPURequest != "" || c.MemoryRequest != "" {
		resources.Requests = corev1.ResourceList{}
		if c.CPURequest != "" {
			cpuRequest, err := resource.ParseQuantity(c.CPURequest)
			if err == nil {
				resources.Requests[corev1.ResourceCPU] = cpuRequest
			}
		}
		if c.MemoryRequest != "" {
			memoryRequest, err := resource.ParseQuantity(c.MemoryRequest)
			if err == nil {
				resources.Requests[corev1.ResourceMemory] = memoryRequest
			}
		}
	}

	if c.CPULimit != "" || c.MemoryLimit != "" {
		resources.Limits = corev1.ResourceList{}
		if c.CPULimit != "" {
			cpuLimit, err := resource.ParseQuantity(c.CPULimit)
			if err == nil {
				resources.Limits[corev1.ResourceCPU] = cpuLimit
			}
		}
		if c.MemoryLimit != "" {
			memoryLimit, err := resource.ParseQuantity(c.MemoryLimit)
			if err == nil {
				resources.Limits[corev1.ResourceMemory] = memoryLimit
			}
		}
	}
	return resources
}

// Int64Ptr returns a pointer to the given int64
func Int64Ptr(i int64) *int64 {
	return &i
}

// AddConfigMapVolumes adds ConfigMap volumes to a pod spec
func AddConfigMapVolumes(podSpec *corev1.PodSpec, container *corev1.Container, configMaps []string) {
	for _, configMapName := range configMaps {
		// Add volume
		podSpec.Volumes = append(
			podSpec.Volumes,
			corev1.Volume{
				Name: fmt.Sprintf("config-%s", configMapName),
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: configMapName,
						},
					},
				},
			},
		)

		// Add volume mount
		container.VolumeMounts = append(
			container.VolumeMounts,
			corev1.VolumeMount{
				Name:      fmt.Sprintf("config-%s", configMapName),
				MountPath: fmt.Sprintf("/etc/config/%s", configMapName),
			},
		)
	}
}

// AddSecretVolumes adds Secret volumes to a pod spec
func AddSecretVolumes(podSpec *corev1.PodSpec, container *corev1.Container, secrets []string) {
	for _, secretName := range secrets {
		// Add volume
		podSpec.Volumes = append(
			podSpec.Volumes,
			corev1.Volume{
				Name: fmt.Sprintf("secret-%s", secretName),
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretName,
					},
				},
			},
		)

		// Add volume mount
		container.VolumeMounts = append(
			container.VolumeMounts,
			corev1.VolumeMount{
				Name:      fmt.Sprintf("secret-%s", secretName),
				MountPath: fmt.Sprintf("/etc/secrets/%s", secretName),
			},
		)
	}
}

type LocalLeaderInfo struct {
	IsLeader    bool            `json:"isLeader"`
	LeaderID    string          `json:"leaderID"`
	LeaderState ControllerState `json:"leaderState"`
	ClusterName string          `json:"clusterName"`
}

func (l LocalLeaderInfo) String() string {
	return fmt.Sprintf("Leader(name=%s, leader=%v, state=%s, cluster=%s)", l.LeaderID, l.IsLeader, l.LeaderState, l.ClusterName)
}

// contextKey is a type to avoid key collisions in context
type contextKey string

const startTimeKey contextKey = "startTime"

// StoreStartTime stores the current time in a new context and returns it
func StoreStartTime(ctx context.Context) context.Context {
	if _, ok := ctx.Value(startTimeKey).(time.Time); ok {
		return ctx
	}
	return context.WithValue(ctx, startTimeKey, time.Now())
}

// GetElapsedTime retrieves the elapsed time from the context
func GetElapsedTime(ctx context.Context) *time.Duration {
	startTime, ok := ctx.Value(startTimeKey).(time.Time)
	if !ok {
		return nil
	}
	elapsed := time.Since(startTime)
	return &elapsed
}

// ControllerState represents the current state of the controller
type ControllerState string

const (
	// StateNormal indicates the controller is operating normally
	StateNormal ControllerState = "Normal"

	// StateDegraded indicates the controller is experiencing issues but still functioning
	StateDegraded ControllerState = "Degraded"
)
