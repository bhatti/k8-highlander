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

// Package service implements the Service workload type for k8-highlander.
//
// This file provides the implementation for continuously running services using
// Kubernetes Deployments. Service workloads are used for long-running processes
// that provide network-accessible APIs or services. They are designed to run
// continuously and handle client requests, with automatic scaling and health
// monitoring provided by k8-highlander.
//
// Key features:
// - Creation and management of Kubernetes Deployments
// - Health monitoring with configurable HTTP health checks
// - Graceful scaling and shutdown
// - Support for ConfigMaps and Secrets
// - Resource management (CPU, memory)
// - Termination grace period control
// - Force deletion of stuck pods during cleanup
//
// Service workloads are ideal for:
// - API servers
// - Web services
// - Application backends
// - Any long-running network service that needs to be singleton
//
// This implementation ensures only one controller manages all service instances,
// preventing duplicate services from running simultaneously while maintaining
// high availability through automatic failover.

package service

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// ServiceWorkload implements a Kubernetes continuously running service workload
// using Deployments for long-running services with network interfaces.
type ServiceWorkload struct {
	config           common.ServiceConfig
	status           common.WorkloadStatus
	statusMutex      sync.RWMutex
	stopCh           chan struct{}
	client           kubernetes.Interface
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewServiceWorkload creates a new deployment workload
func NewServiceWorkload(config common.ServiceConfig, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) (*ServiceWorkload, error) {
	if err := common.ValidationErrors(config.Validate("")); err != nil {
		return nil, err
	}
	// Set defaults
	if config.ReadinessTimeout == 0 {
		config.ReadinessTimeout = 5 * time.Minute
	}
	if config.TerminationGracePeriod == 0 {
		config.TerminationGracePeriod = 28
	}

	return &ServiceWorkload{
		config:           config,
		stopCh:           make(chan struct{}),
		metrics:          metrics,
		monitoringServer: monitoringServer,
		status: common.WorkloadStatus{
			Name:    config.Name,
			Type:    common.WorkloadTypeService,
			Active:  false,
			Healthy: false,
			Details: make(map[string]interface{}),
		},
	}, nil
}

// Start creates or updates the Deployment in Kubernetes and begins health monitoring.
// It ensures the Deployment is properly configured with the right number of replicas.
func (d *ServiceWorkload) Start(ctx context.Context, client kubernetes.Interface) error {
	klog.V(2).Infof("Starting deployment workload %s in namespace %s", d.config.Name, d.config.Namespace)

	startTime := time.Now()
	d.client = client

	// Create or update the deployment
	err := d.createOrUpdateDeployment(ctx)
	duration := time.Since(startTime)

	// Record metrics
	if d.metrics != nil {
		d.metrics.RecordWorkloadOperation(string(d.GetType()), d.GetName(), "startup", duration, err)
	}

	// Update monitoring status
	if d.monitoringServer != nil && err == nil {
		d.monitoringServer.UpdateWorkloadStatus(string(d.GetType()), d.GetName(), d.config.Namespace, true)
	}

	if err != nil {
		klog.Errorf("Failed to start deployment workload %s: %v", d.config.Name, err)

		return fmt.Errorf("failed to create or update deployment: %w", err)
	}

	// Start monitoring the deployment health
	go d.monitorHealth(ctx)
	klog.Infof("Successfully started deployment workload %s in namespace %s [elapsed: %s %v]",
		d.config.Name, d.config.Namespace, time.Since(startTime), d.monitoringServer.GetLeaderInfo())

	return nil
}

// Stop gracefully scales down the Deployment and waits for termination of pods.
// It adds controlled shutdown annotations and forces deletion of stuck pods if necessary.
func (d *ServiceWorkload) Stop(ctx context.Context) error {
	klog.V(2).Infof("Stopping deployment workload %s in namespace %s", d.config.Name, d.config.Namespace)

	startTime := time.Now()

	// Use a mutex to safely close the channel only once
	d.statusMutex.Lock()
	if d.stopCh != nil {
		// Check if channel is already closed by trying to read from it
		select {
		case <-d.stopCh:
			// Channel is already closed, create a new one for future use
			klog.V(4).Infof("Stop channel for %s was already closed, creating a new one", d.config.Name)
			d.stopCh = make(chan struct{})
		default:
			// Channel is still open, close it
			close(d.stopCh)
			d.stopCh = nil
		}
	}
	d.statusMutex.Unlock()

	// Mark the Deployment for controlled shutdown
	deployment, err := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})
	if err == nil {
		// Deployment exists, add shutdown annotation
		klog.Infof("Marking Deployment %s for controlled shutdown", d.config.Name)
		deploymentCopy := deployment.DeepCopy()
		if deploymentCopy.Annotations == nil {
			deploymentCopy.Annotations = make(map[string]string)
		}
		deploymentCopy.Annotations["k8-highlander.io/controlled-shutdown"] = "true"
		_, err = d.client.AppsV1().Deployments(d.config.Namespace).Update(ctx, deploymentCopy, metav1.UpdateOptions{})
		if err != nil {
			klog.Warningf("Failed to mark Deployment %s for controlled shutdown: %v", d.config.Name, err)
		}
	}

	// Scale the deployment to 0
	err = d.scaleDeployment(ctx, 0)
	duration := time.Since(startTime)

	// Record metrics
	if d.metrics != nil {
		d.metrics.RecordWorkloadOperation(string(d.GetType()), d.GetName(), "shutdown", duration, err)
	}

	// Update monitoring status
	if d.monitoringServer != nil && err == nil {
		d.monitoringServer.UpdateWorkloadStatus(string(d.GetType()), d.GetName(), d.config.Namespace, false)
	}

	if err != nil {
		klog.Errorf("Failed to stop deployment workload %s: %v [elapsed: %s]", d.config.Name, err, time.Since(startTime))
		return fmt.Errorf("failed to scale deployment to 0: %w", err)
	}

	waitErr := wait.PollUntilContextTimeout(ctx, 1*time.Second, d.config.TerminationGracePeriod/2+time.Second, true, func(ctx context.Context) (bool, error) {
		// Check if any pods still exist for this deployment
		pods, err := d.client.CoreV1().Pods(d.config.Namespace).List(ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", d.config.Name),
		})

		if err != nil {
			klog.Warningf("Error listing pods for deployment %s: %v [elapsed: %s]", d.config.Name, err, time.Since(startTime))
			return false, nil
		}

		if len(pods.Items) == 0 {
			return true, nil // All pods are gone
		}

		// Force delete any pods that are stuck
		for _, pod := range pods.Items {
			if pod.DeletionTimestamp != nil {
				terminatingTime := time.Since(pod.DeletionTimestamp.Time)
				if terminatingTime > d.config.TerminationGracePeriod/3+time.Second {
					if err = common.ForceDeletePod(ctx, d.client, d.config.Namespace, pod.Name); err != nil {
						klog.Errorf("Pod %s stuck in Terminating state for %v, force deleting failed: %s [elapsed: %s]",
							pod.Name, terminatingTime, err, time.Since(startTime))
					}
				}
			} else {
				// Pod exists but doesn't have a deletion timestamp, try to delete it
				klog.Warningf("Pod %s exists without deletion timestamp, deleting [elapsed: %s]", pod.Name, time.Since(startTime))
				deleteErr := d.client.CoreV1().Pods(d.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
				if deleteErr != nil && !errors.IsNotFound(deleteErr) {
					klog.Errorf("Error deleting pod %s: %v [elapsed: %s]", pod.Name, deleteErr, time.Since(startTime))
				}
			}
		}

		return false, nil // Some pods still exist, keep waiting
	})

	if waitErr != nil {
		klog.Warningf("Timed out waiting for deployment pods to be deleted: %v [elapsed: %s]",
			waitErr, time.Since(startTime))
	}

	klog.Infof("Successfully stopped deployment workload %s in namespace %s [elapsed: %s %v]",
		d.config.Name, d.config.Namespace, time.Since(startTime), d.monitoringServer.GetLeaderInfo())

	return nil
}

func (d *ServiceWorkload) SetMonitoring(metrics *monitoring.ControllerMetrics, monitoringServer *monitoring.MonitoringServer) {
	d.metrics = metrics
	d.monitoringServer = monitoringServer
}

// GetStatus returns the current status of the workload
func (d *ServiceWorkload) GetStatus() common.WorkloadStatus {
	d.statusMutex.RLock()
	defer d.statusMutex.RUnlock()
	return d.status
}

// GetName returns the name of the workload
func (d *ServiceWorkload) GetName() string {
	return d.config.Name
}

// GetType returns the type of the workload
func (d *ServiceWorkload) GetType() common.WorkloadType {
	return common.WorkloadTypeService
}

// createOrUpdateDeployment creates or updates the deployment
func (d *ServiceWorkload) createOrUpdateDeployment(ctx context.Context) error {
	// Convert config to Kubernetes Deployment
	deployment, err := d.buildDeployment()
	if err != nil {
		return fmt.Errorf("failed to create deployment: %w", err)
	}

	// Try to get existing deployment
	existing, err := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check if deployment exists: %w", err)
	}

	if errors.IsNotFound(err) {
		// Create new deployment
		_, err = d.client.AppsV1().Deployments(d.config.Namespace).Create(ctx, deployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("failed to create deployment: %w", err)
		}

		klog.Infof("Created deployment %s in namespace %s, %s",
			d.config.Name, d.config.Namespace, common.ContainersSummary(deployment.Spec.Template.Spec.Containers))
	} else {
		// Update existing deployment
		// Preserve some fields from the existing deployment
		deployment.ResourceVersion = existing.ResourceVersion

		// Update the deployment
		_, err = d.client.AppsV1().Deployments(d.config.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update deployment: %w", err)
		}

		klog.V(4).Infof("Updated deployment %s in namespace %s, %s [%v]",
			d.config.Name, d.config.Namespace,
			common.ContainersSummary(deployment.Spec.Template.Spec.Containers), d.monitoringServer.GetLeaderInfo())
	}

	// Update status
	d.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = true
		s.Details["replicas"] = d.config.Replicas
		s.LastTransition = time.Now()
	})

	return nil
}

// scaleDeployment scales the deployment to the specified number of replicas
func (d *ServiceWorkload) scaleDeployment(ctx context.Context, replicas int32) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the current deployment
		deployment, err := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				klog.Warningf("Deployment %s not found", d.config.Name)
				return nil
			}
			return fmt.Errorf("failed to get deployment: %w", err)
		}

		// Set replicas
		deployment.Spec.Replicas = &replicas

		// Update the deployment
		_, err = d.client.AppsV1().Deployments(d.config.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update deployment: %w", err)
		}

		// Update status
		d.updateStatus(func(s *common.WorkloadStatus) {
			s.Details["replicas"] = replicas
			if replicas == 0 {
				s.Active = false
				s.Healthy = false
			}
		})

		klog.Infof("Scaled deployment %s to %d replicas", d.config.Name, replicas)
		return nil
	})
}

// monitorHealth monitors the health of the deployment
func (d *ServiceWorkload) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			if err := d.checkHealth(ctx); err != nil {
				klog.Errorf("Deployment health check failed %s: %v", d.config.Name, err)

				d.updateStatus(func(s *common.WorkloadStatus) {
					s.Healthy = false
					s.LastError = err.Error()
				})
			} else {
				d.updateStatus(func(s *common.WorkloadStatus) {
					s.Healthy = d.monitoringServer.IsLeaderAndNormal()
					s.LastError = ""
				})
			}
		}
	}
}

// checkHealth checks the health of the deployment with retries
func (d *ServiceWorkload) checkHealth(ctx context.Context) error {
	// Define the health check function that will be retried
	checkFn := func() (bool, string, error) {
		deployment, err := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, "Deployment not found", err
			}
			return false, fmt.Sprintf("Error getting deployment: %v", err), err
		}

		// Check if the deployment has the desired number of replicas available
		desiredReplicas := *deployment.Spec.Replicas
		isActive := desiredReplicas > 0

		// For new deployments, give them time to become ready
		if deployment.Status.ObservedGeneration < deployment.Generation {
			return false, "Deployment still updating to new generation", nil
		}

		// Check if deployment is progressing as expected
		var deploymentMessage string
		for _, condition := range deployment.Status.Conditions {
			if condition.Type == appsv1.DeploymentProgressing {
				if condition.Status != corev1.ConditionTrue {
					deploymentMessage = fmt.Sprintf("Deployment not progressing: %s - %s",
						condition.Reason, condition.Message)
				}
			}
			if condition.Type == appsv1.DeploymentReplicaFailure {
				if condition.Status == corev1.ConditionTrue {
					deploymentMessage = fmt.Sprintf("Deployment replica failure: %s - %s",
						condition.Reason, condition.Message)
				}
			}
		}

		if deploymentMessage != "" {
			return false, deploymentMessage, fmt.Errorf(deploymentMessage)
		}

		// If desired replicas is 0, then the deployment is considered inactive but healthy
		if desiredReplicas == 0 {
			return true, "", nil
		}

		// Otherwise check if enough replicas are ready
		isReady := deployment.Status.ReadyReplicas == desiredReplicas
		if !isReady {
			// Check if we're just waiting for a rollout to complete
			if deployment.Status.UpdatedReplicas < desiredReplicas {
				return false, fmt.Sprintf("Deployment rollout in progress: %d/%d replicas updated",
					deployment.Status.UpdatedReplicas, desiredReplicas), nil
			}

			return false, fmt.Sprintf("Deployment not ready: %d/%d replicas available",
				deployment.Status.ReadyReplicas, desiredReplicas), nil
		}

		return isActive && isReady, "", nil
	}

	// Perform the health check with retries
	healthy, errMsg, err := common.RetryHealthCheck(ctx, 5, 1*time.Second, checkFn)

	// Update status with details
	d.updateStatus(func(s *common.WorkloadStatus) {
		deployment, getErr := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})
		if getErr == nil {
			s.Details["availableReplicas"] = deployment.Status.AvailableReplicas
			s.Details["readyReplicas"] = deployment.Status.ReadyReplicas
			s.Details["updatedReplicas"] = deployment.Status.UpdatedReplicas
			s.Active = *deployment.Spec.Replicas > 0
		}

		s.Healthy = healthy && d.monitoringServer.IsLeaderAndNormal()
		s.LastError = errMsg

		// Update monitoring status
		if d.monitoringServer != nil {
			d.monitoringServer.UpdateWorkloadStatus(string(d.GetType()), d.GetName(), d.config.Namespace, s.Active)
		}
	})

	return err
}

// buildDeployment builds a Kubernetes Deployment from the config
func (d *ServiceWorkload) buildDeployment() (*appsv1.Deployment, error) {
	labels, containers, err := d.config.BuildContainers(d.monitoringServer.GetLeaderInfo())
	if err != nil {
		return nil, err
	}

	terminationSecs := int64(d.config.TerminationGracePeriod.Seconds())
	// Create the deployment
	replicas := d.config.Replicas

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:        d.config.Name,
			Namespace:   d.config.Namespace,
			Labels:      labels,
			Annotations: d.config.Annotations,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": d.config.Name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: d.config.Annotations,
				},
				Spec: corev1.PodSpec{
					TerminationGracePeriodSeconds: &terminationSecs,
					Containers:                    containers,
					NodeSelector:                  d.config.NodeSelector,
				},
			},
		},
	}

	// Add health checks if configured
	if d.config.HealthCheckPath != "" && d.config.HealthCheckPort > 0 {
		deployment.Spec.Template.Spec.Containers[0].ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: d.config.HealthCheckPath,
					Port: intstr.FromInt(int(d.config.HealthCheckPort)),
				},
			},
			InitialDelaySeconds: 10,
			TimeoutSeconds:      5,
			PeriodSeconds:       10,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}

		deployment.Spec.Template.Spec.Containers[0].LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: d.config.HealthCheckPath,
					Port: intstr.FromInt(int(d.config.HealthCheckPort)),
				},
			},
			InitialDelaySeconds: 15,
			TimeoutSeconds:      5,
			PeriodSeconds:       15,
			SuccessThreshold:    1,
			FailureThreshold:    3,
		}
	}

	// Add ConfigMap volumes
	common.AddConfigMapVolumes(&deployment.Spec.Template.Spec, &deployment.Spec.Template.Spec.Containers[0], d.config.ConfigMaps)

	// Add Secret volumes
	common.AddSecretVolumes(&deployment.Spec.Template.Spec, &deployment.Spec.Template.Spec.Containers[0], d.config.Secrets)

	return deployment, nil
}

// updateStatus updates the workload status
func (d *ServiceWorkload) updateStatus(updateFn func(*common.WorkloadStatus)) {
	d.statusMutex.Lock()
	defer d.statusMutex.Unlock()
	updateFn(&d.status)
}

// MonitorDeployment TODO
func (d *ServiceWorkload) MonitorDeployment(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case <-ticker.C:
			deployment, err := d.client.AppsV1().Deployments(d.config.Namespace).Get(ctx, d.config.Name, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					// Deployment doesn't exist, recreate it if we're active
					d.statusMutex.RLock()
					active := d.status.Active
					d.statusMutex.RUnlock()

					if active {
						klog.Warningf("Deployment %s not found, recreating", d.config.Name)
						if err := d.createOrUpdateDeployment(ctx); err != nil {
							klog.Errorf("Failed to recreate Deployment %s: %v", d.config.Name, err)
						}
					}
				} else {
					klog.Errorf("Failed to get Deployment %s: %v", d.config.Name, err)
				}
				continue
			}

			// Check if the Deployment was scaled externally
			if deployment.Spec.Replicas != nil {
				expectedReplicas := int32(0)
				if d.status.Active {
					expectedReplicas = d.config.Replicas
				}

				if *deployment.Spec.Replicas != expectedReplicas {
					klog.Warningf("Deployment %s replicas changed externally (expected: %d, actual: %d), fixing",
						d.config.Name, expectedReplicas, *deployment.Spec.Replicas)
					if err := d.scaleDeployment(ctx, expectedReplicas); err != nil {
						klog.Errorf("Failed to update Deployment replicas %s: %v", d.config.Name, err)
					}
				}
			}
		}
	}
}
