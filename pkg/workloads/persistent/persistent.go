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

// Package persistent implements the StatefulSet workload type for k8-highlander.
//
// This package provides functionality for managing stateful applications that require
// stable network identities and persistent storage. It uses Kubernetes StatefulSets
// to ensure ordered, graceful deployment and scaling with appropriate volume management.
//
// Key features:
// - Creation and management of Kubernetes StatefulSets
// - Support for persistent volumes with configurable storage classes
// - Headless service creation for stable network identities
// - Health monitoring and automatic recovery
// - Graceful scaling and shutdown
// - Support for ConfigMaps and Secrets mounting
// - Resource management (CPU, memory, storage)
//
// Use this workload type for applications that require:
// - Persistent data storage that survives pod restarts
// - Stable, predictable network identities
// - Ordered deployment and scaling
// - State that must be preserved between restarts

package persistent

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
)

// PersistentWorkload implements a Kubernetes StatefulSet workload.
// It manages the lifecycle of a StatefulSet, including creation, updates,
// scaling, health monitoring, and graceful shutdown.
type PersistentWorkload struct {
	config           common.PersistentConfig
	status           common.WorkloadStatus
	statusMutex      sync.RWMutex
	stopCh           chan struct{}
	client           kubernetes.Interface
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewPersistentWorkload creates a new stateful set workload
func NewPersistentWorkload(config common.PersistentConfig, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) (*PersistentWorkload, error) {
	if err := common.ValidationErrors(config.Validate("")); err != nil {
		return nil, err
	}
	// Set defaults
	if config.ReadinessTimeout == 0 {
		config.ReadinessTimeout = 5 * time.Minute
	}
	if config.ServiceName == "" {
		config.ServiceName = config.Name
	}

	return &PersistentWorkload{
		config:           config,
		stopCh:           make(chan struct{}),
		metrics:          metrics,
		monitoringServer: monitoringServer,
		status: common.WorkloadStatus{
			Name:    config.Name,
			Type:    common.WorkloadTypePersistent,
			Active:  false,
			Healthy: false,
			Details: make(map[string]interface{}),
		},
	}, nil
}

// Start creates or updates the StatefulSet and associated service in Kubernetes.
// It also begins health monitoring of the workload.
func (s *PersistentWorkload) Start(ctx context.Context, client kubernetes.Interface) error {
	klog.V(2).Infof("Starting persistent workload %s in namespace %s", s.config.Name, s.config.Namespace)

	startTime := time.Now()
	s.client = client

	// Create service if needed
	if len(s.config.Ports) > 0 {
		if err := s.createOrUpdateService(ctx); err != nil {
			if s.metrics != nil {
				s.metrics.RecordWorkloadOperation(string(s.GetType()), s.GetName(), "startup", time.Since(startTime), err)
			}
			return fmt.Errorf("failed to create or update service: %w", err)
		}
	}

	// Create or update the stateful set
	err := s.createOrUpdateStatefulSet(ctx)
	duration := time.Since(startTime)

	// Record metrics
	if s.metrics != nil {
		s.metrics.RecordWorkloadOperation(string(s.GetType()), s.GetName(), "startup", duration, err)
	}

	// Update monitoring status
	if s.monitoringServer != nil && err == nil {
		s.monitoringServer.UpdateWorkloadStatus(string(s.GetType()), s.GetName(), s.config.Namespace, true)
	}

	if err != nil {
		klog.Errorf("Failed to start persistent workload %s: %v", s.config.Name, err)

		return fmt.Errorf("failed to create or update persistent set: %w", err)
	}

	// Start monitoring the stateful set health
	go s.monitorHealth(ctx)

	klog.Infof("Successfully started persistent workload %s in namespace %s [elapsed: %s %v]",
		s.config.Name, s.config.Namespace, time.Since(startTime), s.monitoringServer.GetLeaderInfo())

	return nil
}

// Stop gracefully scales down the StatefulSet and waits for termination.
// It adds controlled shutdown annotations and forces deletion of stuck pods if necessary.
func (s *PersistentWorkload) Stop(ctx context.Context) error {
	klog.V(2).Infof("Stopping persistent workload %s in namespace %s", s.config.Name, s.config.Namespace)

	startTime := time.Now()

	// Use a mutex to safely close the channel only once
	s.statusMutex.Lock()
	if s.stopCh != nil {
		// Check if channel is already closed by trying to read from it
		select {
		case <-s.stopCh:
			// Channel is already closed, create a new one for future use
			klog.V(4).Infof("Stop channel for %s was already closed, creating a new one", s.config.Name)
			s.stopCh = make(chan struct{})
		default:
			// Channel is still open, close it
			close(s.stopCh)
			s.stopCh = nil
		}
	}
	s.statusMutex.Unlock()

	// Mark the StatefulSet for controlled shutdown
	statefulSet, err := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})
	if err == nil {
		// StatefulSet exists, add shutdown annotation
		klog.Infof("Marking StatefulSet %s for controlled shutdown", s.config.Name)
		statefulSetCopy := statefulSet.DeepCopy()
		if statefulSetCopy.Annotations == nil {
			statefulSetCopy.Annotations = make(map[string]string)
		}
		statefulSetCopy.Annotations["k8-highlander.io/controlled-shutdown"] = "true"
		_, err = s.client.AppsV1().StatefulSets(s.config.Namespace).Update(ctx, statefulSetCopy, metav1.UpdateOptions{})
		if err != nil {
			klog.Warningf("Failed to mark StatefulSet %s for controlled shutdown: %v", s.config.Name, err)
		}
	}

	// Scale the stateful set to 0
	err = s.scaleStatefulSet(ctx, 0)
	duration := time.Since(startTime)

	// Record metrics
	if s.metrics != nil {
		s.metrics.RecordWorkloadOperation(string(s.GetType()), s.GetName(), "shutdown", duration, err)
	}

	// Update monitoring status
	if s.monitoringServer != nil {
		s.monitoringServer.UpdateWorkloadStatus(string(s.GetType()), s.GetName(), s.config.Namespace, false)
	}

	if err != nil {
		klog.Errorf("Failed to stop persistent workload %s: %v", s.config.Name, err)
		return fmt.Errorf("failed to scale persistent set to 0: %w", err)
	}

	waitErr := wait.PollUntilContextTimeout(ctx, 1*time.Second, s.config.TerminationGracePeriod/2+time.Second, true, func(ctx context.Context) (bool, error) {
		// Check if any pods still exist for this stateful set
		pods, err := s.client.CoreV1().Pods(s.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", s.config.Name),
		})

		if err != nil {
			klog.Warningf("Error listing pods for persistent set %s: %v [elapsed: %s]", s.config.Name, err, time.Since(startTime))
			return false, nil
		}

		if len(pods.Items) == 0 {
			return true, nil // All pods are gone
		}

		// Force delete any pods that are stuck
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil {
				terminatingTime := time.Since(pod.DeletionTimestamp.Time)
				if terminatingTime > 10*time.Second {
					if err = common.ForceDeletePod(ctx, s.client, s.config.Namespace, pod.Name); err != nil {
						klog.Errorf("Pod %s stuck in Terminating state for %v, force deleting failed: %s [elapsed: %s]",
							pod.Name, terminatingTime, err, time.Since(startTime))
					}
				}
			} else {
				// Pod exists but doesn't have a deletion timestamp, try to delete it
				klog.Warningf("Pod %s exists without deletion timestamp, deleting", pod.Name)
				deleteErr := s.client.CoreV1().Pods(s.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
				if deleteErr != nil && !errors.IsNotFound(deleteErr) {
					klog.Errorf("Error deleting pod %s: %v", pod.Name, deleteErr)
				}
			}
		}

		return false, nil // Some pods still exist, keep waiting
	})

	if waitErr != nil {
		klog.Warningf("Timed out waiting for persistent set pods to be deleted: %v [elapsed: %s]", waitErr, time.Since(startTime))
	}

	klog.Infof("Successfully stopped persistent workload %s in namespace %s [elapsed: %s]", s.config.Name, s.config.Namespace, time.Since(startTime))

	return nil
}

func (s *PersistentWorkload) SetMonitoring(metrics *monitoring.ControllerMetrics, monitoringServer *monitoring.MonitoringServer) {
	s.metrics = metrics
	s.monitoringServer = monitoringServer
}

// GetStatus returns the current status of the workload
func (s *PersistentWorkload) GetStatus() common.WorkloadStatus {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()
	return s.status
}

// GetName returns the name of the workload
func (s *PersistentWorkload) GetName() string {
	return s.config.Name
}

// GetType returns the type of the workload
func (s *PersistentWorkload) GetType() common.WorkloadType {
	return common.WorkloadTypePersistent
}

// createOrUpdateService creates or updates the service for the persistent set
func (s *PersistentWorkload) createOrUpdateService(ctx context.Context) error {
	// Build service
	service := s.buildService()

	// Try to get existing service
	existing, err := s.client.CoreV1().Services(s.config.Namespace).Get(ctx, s.config.ServiceName, metav1.GetOptions{})

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check if service exists: %w", err)
	}

	if errors.IsNotFound(err) {
		// Create new service
		_, err = s.client.CoreV1().Services(s.config.Namespace).Create(ctx, service, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create service: %w", err)
		}

		klog.Infof("Created service %s in namespace %s", s.config.ServiceName, s.config.Namespace)
	} else {
		// Update existing service
		// Preserve cluster IP and other fields
		service.Spec.ClusterIP = existing.Spec.ClusterIP
		service.ResourceVersion = existing.ResourceVersion

		// Update the service
		_, err = s.client.CoreV1().Services(s.config.Namespace).Update(ctx, service, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update service: %w", err)
		}

		klog.V(4).Infof("Updated service %s in namespace %s [%v]",
			s.config.ServiceName, s.config.Namespace, s.monitoringServer.GetLeaderInfo())
	}

	return nil
}

// createOrUpdateStatefulSet creates or updates the persistent set
func (s *PersistentWorkload) createOrUpdateStatefulSet(ctx context.Context) error {
	// Build persistent set
	statefulSet, err := s.buildStatefulSet()
	if err != nil {
		return fmt.Errorf("failed to create persistent set: %w", err)
	}

	// Try to get existing stateful set
	existing, err := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check if persistent set exists: %w", err)
	}

	if errors.IsNotFound(err) {
		// Create new stateful set
		_, err = s.client.AppsV1().StatefulSets(s.config.Namespace).Create(ctx, statefulSet, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create persistent set: %w", err)
		}

		klog.Infof("Created persistent set %s in namespace %s, %s",
			s.config.Name, s.config.Namespace, common.ContainersSummary(statefulSet.Spec.Template.Spec.Containers))
	} else {
		// Update existing stateful set
		// Preserve some fields
		statefulSet.ResourceVersion = existing.ResourceVersion

		// Update the stateful set
		_, err = s.client.AppsV1().StatefulSets(s.config.Namespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update persistent set: %w", err)
		}

		klog.Infof("Updated persistent set %s in namespace %s, %s [%v]",
			s.config.Name, s.config.Namespace,
			common.ContainersSummary(statefulSet.Spec.Template.Spec.Containers), s.monitoringServer.GetLeaderInfo())
	}

	// Update status
	s.updateStatus(func(status *common.WorkloadStatus) {
		status.Active = true
		status.Details["replicas"] = s.config.Replicas
		status.LastTransition = time.Now()
	})

	return nil
}

// scaleStatefulSet scales the stateful set to the specified number of replicas
func (s *PersistentWorkload) scaleStatefulSet(ctx context.Context, replicas int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the current stateful set
		statefulSet, err := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("StatefulSet %s not found", s.config.Name)
				return nil
			}
			return fmt.Errorf("failed to get persistent set: %w", err)
		}

		// Set replicas
		statefulSet.Spec.Replicas = &replicas

		// Update the stateful set
		_, err = s.client.AppsV1().StatefulSets(s.config.Namespace).Update(ctx, statefulSet, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update persistent set: %w", err)
		}

		// Update status
		s.updateStatus(func(status *common.WorkloadStatus) {
			status.Details["replicas"] = replicas
			if replicas == 0 {
				status.Active = false
			}
		})

		klog.Infof("Scaled persistent set %s to %d replicas", s.config.Name, replicas)
		return nil
	})
}

// monitorHealth monitors the health of the stateful set
func (s *PersistentWorkload) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			if err := s.checkHealth(ctx); err != nil {
				klog.Errorf("StatefulSet health check failed: %v", err)

				s.updateStatus(func(status *common.WorkloadStatus) {
					status.Healthy = false
					status.LastError = err.Error()
				})
			} else {
				s.updateStatus(func(status *common.WorkloadStatus) {
					status.Healthy = s.monitoringServer.IsLeaderAndNormal()
					status.LastError = ""
				})
			}
		}
	}
}

// checkHealth checks the health of the stateful set with retries
func (s *PersistentWorkload) checkHealth(ctx context.Context) error {
	// Define the health check function that will be retried
	checkFn := func() (bool, string, error) {
		statefulSet, err := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, "StatefulSet not found", err
			}
			return false, fmt.Sprintf("Error getting StatefulSet: %v", err), err
		}

		// Check if the stateful set has the desired number of replicas ready
		desiredReplicas := *statefulSet.Spec.Replicas
		isActive := desiredReplicas > 0

		// For new StatefulSets, give them time to become ready
		if statefulSet.Status.ObservedGeneration < statefulSet.Generation {
			return false, "StatefulSet still updating to new generation", nil
		}

		// If desired replicas is 0, then the StatefulSet is considered inactive but healthy
		if desiredReplicas == 0 {
			return true, "", nil
		}

		// Check conditions for the StatefulSet
		isReady := statefulSet.Status.ReadyReplicas == desiredReplicas
		if !isReady {
			// For StatefulSets, check if pods are still creating
			if statefulSet.Status.CurrentReplicas < desiredReplicas {
				return false, fmt.Sprintf("StatefulSet scaling up: %d/%d replicas created",
					statefulSet.Status.CurrentReplicas, desiredReplicas), nil
			}

			if statefulSet.Status.UpdatedReplicas < desiredReplicas {
				return false, fmt.Sprintf("StatefulSet update in progress: %d/%d replicas updated",
					statefulSet.Status.UpdatedReplicas, desiredReplicas), nil
			}

			return false, fmt.Sprintf("StatefulSet not ready: %d/%d replicas ready",
				statefulSet.Status.ReadyReplicas, desiredReplicas), nil
		}

		return isActive && isReady, "", nil
	}

	// Perform the health check with retries
	healthy, errMsg, err := common.RetryHealthCheck(ctx, 5, 1*time.Second, checkFn)

	// Update status with details
	s.updateStatus(func(status *common.WorkloadStatus) {
		statefulSet, getErr := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})
		if getErr == nil {
			status.Details["readyReplicas"] = statefulSet.Status.ReadyReplicas
			status.Details["currentReplicas"] = statefulSet.Status.CurrentReplicas
			status.Details["updatedReplicas"] = statefulSet.Status.UpdatedReplicas
			status.Active = *statefulSet.Spec.Replicas > 0
		}

		status.Healthy = healthy && s.monitoringServer.IsLeaderAndNormal()
		status.LastError = errMsg

		// Update monitoring status
		if s.monitoringServer != nil {
			s.monitoringServer.UpdateWorkloadStatus(string(s.GetType()), s.GetName(), s.config.Namespace, status.Active)
		}
	})

	return err
}

func (s *PersistentWorkload) buildService() *corev1.Service {
	// Set up labels
	labels := make(map[string]string)
	for k, v := range s.config.Labels {
		labels[k] = v
	}
	labels["app"] = s.config.Name
	labels["managed-by"] = "k8-highlander"

	// Set up ports
	var ports []corev1.ServicePort
	for _, portConfig := range s.config.Ports {
		servicePort := portConfig.ServicePort
		if servicePort == 0 {
			servicePort = portConfig.ContainerPort
		}

		ports = append(ports, corev1.ServicePort{
			Name:       portConfig.Name,
			Port:       servicePort,
			TargetPort: intstr.FromInt(int(portConfig.ContainerPort)),
		})
	}

	// Create the service
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.config.ServiceName,
			Namespace:   s.config.Namespace,
			Labels:      labels,
			Annotations: s.config.Annotations,
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{
				"app": s.config.Name,
			},
			Ports: ports,
			Type:  corev1.ServiceTypeClusterIP,
		},
	}

	return service
}

// buildStatefulSet builds a Kubernetes StatefulSet from the config
func (s *PersistentWorkload) buildStatefulSet() (*appsv1.StatefulSet, error) {
	labels, containers, err := s.config.BuildContainers(s.monitoringServer.GetLeaderInfo())
	if err != nil {
		return nil, err
	}
	// Create the stateful set
	replicas := s.config.Replicas
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:        s.config.Name,
			Namespace:   s.config.Namespace,
			Labels:      labels,
			Annotations: s.config.Annotations,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": s.config.Name,
				},
			},
			ServiceName: s.config.ServiceName,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: s.config.Annotations,
				},
				Spec: corev1.PodSpec{
					Containers:   containers,
					NodeSelector: s.config.NodeSelector,
				},
			},
		},
	}

	// Add health checks if configured
	if s.config.HealthCheckPath != "" && s.config.HealthCheckPort > 0 {
		statefulSet.Spec.Template.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: s.config.HealthCheckPath,
					Port: intstr.FromInt(int(s.config.HealthCheckPort)),
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      5,
			PeriodSeconds:       10,
		}

		statefulSet.Spec.Template.Spec.Containers[0].LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: s.config.HealthCheckPath,
					Port: intstr.FromInt(int(s.config.HealthCheckPort)),
				},
			},
			InitialDelaySeconds: 12,
			TimeoutSeconds:      5,
			PeriodSeconds:       30,
		}
	}
	// Add ConfigMap volumes
	common.AddConfigMapVolumes(&statefulSet.Spec.Template.Spec, &statefulSet.Spec.Template.Spec.Containers[0], s.config.ConfigMaps)

	// Add Secret volumes
	common.AddSecretVolumes(&statefulSet.Spec.Template.Spec, &statefulSet.Spec.Template.Spec.Containers[0], s.config.Secrets)

	// Add persistent volume claims
	if len(s.config.PersistentVolumes) > 0 {
		var volumeClaimTemplates []corev1.PersistentVolumeClaim

		for _, volumeConfig := range s.config.PersistentVolumes {
			// Create storage request
			storageRequest, err := resource.ParseQuantity(volumeConfig.Size)
			if err != nil {
				klog.Errorf("Failed to parse storage size %s: %v", volumeConfig.Size, err)
				continue
			}

			// Create volume claim template
			volumeClaimTemplate := corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name: volumeConfig.Name,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: storageRequest,
						},
					},
				},
			}

			// Set storage class if specified
			if volumeConfig.StorageClassName != "" {
				volumeClaimTemplate.Spec.StorageClassName = &volumeConfig.StorageClassName
			}

			volumeClaimTemplates = append(volumeClaimTemplates, volumeClaimTemplate)

			// Add volume mount
			statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts = append(
				statefulSet.Spec.Template.Spec.Containers[0].VolumeMounts,
				corev1.VolumeMount{
					Name:      volumeConfig.Name,
					MountPath: volumeConfig.MountPath,
				},
			)
		}

		statefulSet.Spec.VolumeClaimTemplates = volumeClaimTemplates
	}

	return statefulSet, nil
}

// updateStatus updates the workload status
func (s *PersistentWorkload) updateStatus(updateFn func(*common.WorkloadStatus)) {
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	updateFn(&s.status)
}

// MonitorStatefulSet TODO
func (s *PersistentWorkload) MonitorStatefulSet(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			statefulSet, err := s.client.AppsV1().StatefulSets(s.config.Namespace).Get(ctx, s.config.Name, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					// StatefulSet doesn't exist, recreate it if we're active
					s.statusMutex.RLock()
					active := s.status.Active
					s.statusMutex.RUnlock()

					if active {
						klog.Warningf("StatefulSet %s not found, recreating", s.config.Name)
						if err := s.createOrUpdateStatefulSet(ctx); err != nil {
							klog.Errorf("Failed to recreate StatefulSet: %v", err)
						}
					}
				} else {
					klog.Errorf("Failed to get StatefulSet %s: %v", s.config.Name, err)
				}
				continue
			}

			// Check if the StatefulSet was scaled externally
			if statefulSet.Spec.Replicas != nil {
				expectedReplicas := int32(0)
				if s.status.Active {
					expectedReplicas = s.config.Replicas
				}

				if *statefulSet.Spec.Replicas != expectedReplicas {
					klog.Warningf("StatefulSet %s replicas changed externally (expected: %d, actual: %d), fixing",
						s.config.Name, expectedReplicas, *statefulSet.Spec.Replicas)
					if err := s.scaleStatefulSet(ctx, expectedReplicas); err != nil {
						klog.Errorf("Failed to update StatefulSet replicas: %v", err)
					}
				}
			}
		}
	}
}
