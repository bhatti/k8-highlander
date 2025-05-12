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

// Package process implements the Process workload type for k8-highlander.
//
// This file provides the implementation for single-instance process workloads
// using Kubernetes Pods. Process workloads represent individual, potentially
// long-running processes that should only run in a single instance across
// the entire cluster to avoid duplication or race conditions.
//
// Key features:
// - Creation and management of Kubernetes Pods
// - Automatic recreation of terminated Pods
// - Controlled shutdown with graceful termination
// - Continuous monitoring of Pod health and status
// - Advanced termination handling for stuck Pods
// - Support for ConfigMaps and Secrets
// - Finalizers to ensure proper cleanup
//
// Process workloads are ideal for:
// - Batch processing jobs
// - Data import/export operations
// - System maintenance tasks
// - Legacy applications that cannot be horizontally scaled
// - Any process that needs exclusive access to resources
//
// This implementation ensures that only one controller manages the process
// and automatically handles Pod recreation if it terminates unexpectedly
// while under management.

package process

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	"github.com/oklog/ulid/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/klog/v2"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
)

// ProcessWorkload implements a process workload using Kubernetes Pods.
// It manages the lifecycle of a single Pod, monitoring its health and status,
// and ensuring it's recreated if terminated unexpectedly.
type ProcessWorkload struct {
	config           common.ProcessConfig
	status           common.WorkloadStatus
	statusMutex      sync.RWMutex
	stopCh           chan struct{}
	stopped          bool
	client           kubernetes.Interface
	namespace        string
	podName          string
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewProcessWorkload creates a new process workload
func NewProcessWorkload(config common.ProcessConfig, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer, client kubernetes.Interface) (*ProcessWorkload, error) {
	if err := common.ValidationErrors(config.Validate("")); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("kubernetes client not specified")
	}
	// Set defaults
	if config.TerminationGracePeriod == 0 {
		config.TerminationGracePeriod = 28 * time.Second
	}
	if config.RestartPolicy == "" {
		config.RestartPolicy = "Never" // OnFailure - this recreates pod on stop
	}
	config.ID = ulid.Make().String()

	return &ProcessWorkload{
		config:           config,
		stopCh:           make(chan struct{}),
		namespace:        config.Namespace,
		podName:          fmt.Sprintf("%s-pod", config.Name),
		metrics:          metrics,
		monitoringServer: monitoringServer,
		client:           client,
		status: common.WorkloadStatus{
			Name:    config.Name,
			Type:    common.WorkloadTypeProcess,
			Active:  false,
			Healthy: false,
			Details: make(map[string]interface{}),
		},
	}, nil
}

func (p *ProcessWorkload) String() string {
	p.statusMutex.RLock()
	defer p.statusMutex.RUnlock()
	return fmt.Sprintf("ProcessWorkload(id=%s, name=%s, active=%v, healthy=%v, image=%s)",
		p.config.ID, p.config.Name, p.status.Active, p.status.Healthy, p.config.Image)
}

// Start creates or updates the Pod in Kubernetes and begins monitoring its health.
// It also sets up a watcher to detect unexpected Pod terminations.
func (p *ProcessWorkload) Start(ctx context.Context) error {
	klog.V(2).Infof("Starting process workload %s in namespace %s", p, p.namespace)

	startTime := time.Now()

	// Create and start the pod
	err := p.createOrUpdatePod(ctx)
	duration := time.Since(startTime)

	// Record metrics
	if p.metrics != nil {
		p.metrics.RecordWorkloadOperation(string(p.GetType()), p.GetName(), "startup", duration, err)
	}

	// Update monitoring status
	if p.monitoringServer != nil && err == nil {
		p.monitoringServer.UpdateWorkloadStatus(string(p.GetType()), p.GetName(), p.namespace, true)
	}

	if err != nil {
		klog.Errorf("Failed to start process workload %s: %v", p, err)

		return fmt.Errorf("failed to create or update pod: %w", err)
	}

	// Start monitoring the pod for status updates
	go p.periodicStatusUpdater(ctx)

	// Start watching for pod terminations
	go p.watchPodTerminations(ctx)

	klog.Infof("Successfully started process workload %s in namespace %s [elapsed: %s]",
		p, p.namespace, time.Since(startTime))

	return nil
}

// Stop gracefully terminates the Pod, adding controlled shutdown annotations
// and forcing deletion of stuck Pods if necessary.
func (p *ProcessWorkload) Stop(ctx context.Context) error {
	klog.V(2).Infof("Stopping process workload %s in namespace %s", p, p.namespace)

	startTime := time.Now()

	// Use a mutex to safely close the channel only once
	p.statusMutex.Lock()
	if p.stopCh != nil && !p.stopped {
		// Check if channel is already closed by trying to read from it
		select {
		case <-p.stopCh:
			// Channel is already closed, create a new one for future use
			klog.V(4).Infof("Stop channel for %s was already closed, creating a new one", p)
			p.stopCh = make(chan struct{})
		default:
			// Channel is still open, close it
			close(p.stopCh)
		}
		p.stopped = true
	}
	p.statusMutex.Unlock()

	// First, mark the pod for controlled shutdown
	if p.client == nil {
		klog.Warningf("Cannot stop workload %s: client not initialized", p)

		// Update status
		p.updateStatus(func(s *common.WorkloadStatus) {
			s.Active = false
			s.Healthy = false
			s.Details["stoppedAt"] = time.Now()
		})
		panic("nil client")
		return nil
	}
	pod, err := p.client.CoreV1().Pods(p.namespace).Get(ctx, p.podName, metav1.GetOptions{})
	if err == nil {
		// Pod exists, add shutdown annotation
		klog.Infof("Marking pod %s for controlled shutdown", p.podName)
		podCopy := pod.DeepCopy()
		if podCopy.Annotations == nil {
			podCopy.Annotations = make(map[string]string)
		}
		podCopy.Annotations["k8-highlander.io/controlled-shutdown"] = "true"
		_, err = p.client.CoreV1().Pods(p.namespace).Update(ctx, podCopy, metav1.UpdateOptions{})
		if err != nil {
			klog.Warningf("Failed to mark pod %s for controlled shutdown: %v", p.podName, err)
		}

		// Wait a moment for the annotation to propagate
		time.Sleep(100 * time.Millisecond)
	}

	// Delete the pod
	gracePeriodSeconds := int64(p.config.TerminationGracePeriod.Seconds())
	err = common.RetryWithBackoff(ctx, "delete pod", func() error {
		return p.client.CoreV1().Pods(p.namespace).Delete(ctx, p.podName, metav1.DeleteOptions{
			GracePeriodSeconds: &gracePeriodSeconds,
		})
	})

	// If the pod was deleted or not found, wait for it to be fully gone
	if err == nil || errors.IsNotFound(err) {
		waitErr := wait.PollUntilContextTimeout(ctx, 1*time.Second, p.config.TerminationGracePeriod/2+time.Second, true, func(ctx context.Context) (bool, error) {
			pod, err := p.client.CoreV1().Pods(p.namespace).Get(ctx, p.podName, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return true, nil // Pod is gone, we're done
			}
			if err != nil {
				klog.Warningf("Error checking pod %s status: %v [elapsed: %s]", p.podName, err,
					time.Since(startTime))
				return false, nil // Some other error, keep waiting
			}

			// If pod is stuck in Terminating state for too long, force delete it
			if pod.DeletionTimestamp != nil {
				terminatingTime := time.Since(pod.DeletionTimestamp.Time)
				if terminatingTime > 10*time.Second {
					if err = common.ForceDeletePod(ctx, p.client, p.config.Namespace, pod.Name); err != nil {
						klog.Errorf("Pod %s stuck in Terminating state for %v, force deleting failed: %s [elapsed: %s]",
							pod.Name, terminatingTime, err, time.Since(startTime))
					}
				}
			}

			return false, nil // Pod still exists, keep waiting
		})

		if waitErr != nil {
			klog.Warningf("Timed out waiting for pod %s to be deleted: %v [elapsed: %s]",
				p.podName, waitErr, time.Since(startTime))

			// One last attempt to force delete
			zero := int64(0)
			forceErr := common.RetryWithBackoff(ctx, "final force delete pod", func() error {
				return p.client.CoreV1().Pods(p.namespace).Delete(ctx, p.podName, metav1.DeleteOptions{
					GracePeriodSeconds: &zero,
				})
			})

			if forceErr != nil && !errors.IsNotFound(forceErr) {
				klog.Errorf("Final force delete of pod %s failed: %v [elapsed %s]", p.podName, forceErr, time.Since(startTime))
			}
		}
	}

	duration := time.Since(startTime)

	// Record metrics
	if p.metrics != nil {
		p.metrics.RecordWorkloadOperation(string(p.GetType()), p.GetName(), "shutdown", duration, err)
	}

	// Update monitoring status
	if p.monitoringServer != nil && err == nil {
		p.monitoringServer.UpdateWorkloadStatus(string(p.GetType()), p.GetName(), p.namespace, false)
	}

	if err != nil && !errors.IsNotFound(err) {
		klog.Errorf("Failed to stop process workload %s: %v [elapsed: %s]", p, err, time.Since(startTime))
		return fmt.Errorf("failed to delete pod: %w", err)
	}

	// Update status
	p.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = false
		s.Healthy = false
		s.Details["stoppedAt"] = time.Now()
	})

	klog.Infof("Successfully stopped process workload %s in namespace %s [elapsed: %s]", p, p.namespace, time.Since(startTime))

	return nil
}

// GetConfig returns a workload info
func (p *ProcessWorkload) GetConfig() common.BaseWorkloadConfig {
	return p.config.BaseWorkloadConfig
}

func (p *ProcessWorkload) SetMonitoring(
	metrics *monitoring.ControllerMetrics, monitoringServer *monitoring.MonitoringServer) {
	p.metrics = metrics
	p.monitoringServer = monitoringServer
}

// GetStatus returns the current status of the workload
func (p *ProcessWorkload) GetStatus() common.WorkloadStatus {
	p.statusMutex.RLock()
	defer p.statusMutex.RUnlock()
	return p.status
}

// GetName returns the name of the workload
func (p *ProcessWorkload) GetName() string {
	return p.config.Name
}

// GetType returns the type of the workload
func (p *ProcessWorkload) GetType() common.WorkloadType {
	return common.WorkloadTypeProcess
}

func (p *ProcessWorkload) watchPodTerminations(ctx context.Context) {
	// Create a watcher for this specific pod
	watcher, err := p.client.CoreV1().Pods(p.namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", p.podName),
	})
	if err != nil {
		klog.Errorf("Failed to create pod watcher for %s: %v", p.podName, err)
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case event, ok := <-watcher.ResultChan():
			if !ok {
				klog.Warningf("Pod watcher channel closed for %s, restarting watcher", p.podName)
				// Restart the watcher after a short delay
				time.Sleep(5 * time.Second)
				go p.watchPodTerminations(ctx)
				return
			}

			if event.Type == watch.Deleted {
				pod, ok := event.Object.(*corev1.Pod)
				if !ok {
					continue
				}

				// Check if this was a controlled shutdown
				controlledShutdown := false
				if pod.Annotations != nil {
					_, controlledShutdown = pod.Annotations["k8-highlander.io/controlled-shutdown"]
				}

				if !controlledShutdown {
					klog.Warningf("Pod %s was deleted unexpectedly, recreating %s in namespace %s [%v]", p.podName,
						p, p.namespace, p.monitoringServer.GetLeaderInfo())
					// Recreate the pod
					if err := p.createOrUpdatePod(ctx); err != nil {
						klog.Errorf("Failed to recreate pod %s: %v", p.podName, err)
					}
				} else {
					debug.PrintStack()
					klog.Infof("Pod %s was deleted as part of controlled shutdown %s in namespace %s [%v]", p.podName,
						p, p.namespace, p.monitoringServer.GetLeaderInfo())
				}
			}
		}
	}
}

// createOrUpdatePod creates or updates the pod for the process
func (p *ProcessWorkload) createOrUpdatePod(ctx context.Context) error {
	klog.Infof("Creating/updating pod for process %s (image %s) in namespace %s [%v]",
		p, p.config.Image, p.namespace, p.monitoringServer.GetLeaderInfo())
	startTime := time.Now()
	// Build the pod
	pod, err := p.buildPod()
	if err != nil {
		return fmt.Errorf("failed to create pod: %w", err)
	}

	// Check if pod already exists
	existing, err := p.client.CoreV1().Pods(p.namespace).Get(ctx, p.podName, metav1.GetOptions{})

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to check if pod exists: %w", err)
	}

	if errors.IsNotFound(err) {
		// Create new pod
		klog.Infof("Pod %s does not exist, creating it %s in namespace %s [%v]", p.podName,
			p, p.namespace, p.monitoringServer.GetLeaderInfo())

		_, err = p.client.CoreV1().Pods(p.namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("Failed to create pod %s: %v", p.podName, err)

			return fmt.Errorf("failed to create pod: %w", err)
		}

		klog.Infof("Created pod %s (image:%s) in namespace %s, %s [elapsed: %s]",
			p.podName, p.config.Image, p.namespace, common.ContainersSummary(pod.Spec.Containers), time.Since(startTime))
	} else {
		// If pod exists but is in a terminal state, delete it and create a new one
		if isPodTerminated(existing) {
			klog.Infof("Pod %s is terminated, recreating it [%v]", p.podName, p.monitoringServer.GetLeaderInfo())

			err = p.client.CoreV1().Pods(p.namespace).Delete(ctx, p.podName, metav1.DeleteOptions{})
			if err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("failed to delete terminated pod: %w", err)
			}

			// Create new pod
			created, err := p.client.CoreV1().Pods(p.namespace).Create(ctx, pod, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create pod: %w", err)
			}
			klog.Infof("Recreated pod %s in namespace %s with status %s, %s [elapsed: %s]",
				created.Name, created.Namespace, created.Status.Phase, common.ContainersSummary(pod.Spec.Containers), time.Since(startTime))
		} else {
			// Pod exists and is running, nothing to do
			klog.Infof("Pod %s already exists in namespace %s with status %s, %s [elapsed: %s]",
				existing.Name, existing.Namespace, existing.Status.Phase, common.ContainersSummary(pod.Spec.Containers), time.Since(startTime))
		}
	}

	// Update status
	p.updateStatus(func(s *common.WorkloadStatus) {
		s.Active = true
		s.Details["startedAt"] = time.Now()
	})

	return nil
}

// checkHealth checks the health of the pod with retries
func (p *ProcessWorkload) checkHealth(ctx context.Context) error {
	// Define the actual health check function that will be retried
	checkFn := func() (bool, string, error) {
		pod, err := p.client.CoreV1().Pods(p.namespace).Get(ctx, p.podName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				// Only consider a missing pod an error if it's been missing for some time
				p.statusMutex.RLock()
				lastTransition := p.status.LastTransition
				p.statusMutex.RUnlock()

				if time.Since(lastTransition) < 30*time.Second {
					// Pod might still be creating or in transition, don't mark as error yet
					return false, "Pod not found, but within grace period", nil
				}

				return false, fmt.Sprintf("Pod not found: %v", err), err
			}
			return false, fmt.Sprintf("Error getting pod: %v", err), err
		}

		// Check if pod is running and ready
		if pod.Status.Phase == corev1.PodRunning {
			// Check if all containers are ready
			allReady := true
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if !containerStatus.Ready {
					allReady = false
					break
				}
			}

			if allReady {
				return true, "", nil
			} else {
				return false, "Not all containers are ready", nil
			}
		} else if pod.Status.Phase == corev1.PodSucceeded {
			// For jobs that complete successfully
			return true, "", nil
		} else if pod.Status.Phase == corev1.PodFailed {
			// For pods that failed, get container error info
			errorMsg := "Pod failed"
			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.State.Terminated != nil && containerStatus.State.Terminated.ExitCode != 0 {
					errorMsg = fmt.Sprintf("Container %s terminated with exit code %d: %s",
						containerStatus.Name,
						containerStatus.State.Terminated.ExitCode,
						containerStatus.State.Terminated.Message)
					break
				}
			}
			return false, errorMsg, fmt.Errorf("failed to get pod info: %s", errorMsg)
		} else if pod.Status.Phase == corev1.PodPending {
			// Check if pod is still being created (give it more time)
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					return false, fmt.Sprintf("Pod in Pending state: %s", condition.Reason), nil
				}
			}
			return false, fmt.Sprintf("Pod in Pending state"), nil
		}

		return false, fmt.Sprintf("Pod in %s state", pod.Status.Phase), nil
	}

	// Perform the health check with retries
	healthy, errMsg, err := common.RetryHealthCheck(ctx, 5, 1*time.Second, checkFn)

	// Update status based on health check results
	p.updateStatus(func(s *common.WorkloadStatus) {
		s.Healthy = healthy && p.monitoringServer.IsLeaderAndNormal()
		s.LastError = errMsg
	})

	return err
}

// buildPod builds a Kubernetes Pod for the process
func (p *ProcessWorkload) buildPod() (*corev1.Pod, error) {
	labels, containers, err := p.config.BuildContainers(p.monitoringServer.GetLeaderInfo())
	if err != nil {
		return nil, err
	}
	// Create the pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        p.podName,
			Namespace:   p.namespace,
			Labels:      labels,
			Annotations: p.config.Annotations,
			// Optionally add a finalizer to prevent immediate deletion
			// Finalizers: []string{"k8-highlander.io/managed-pod"},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicy(p.config.RestartPolicy),
			Containers:    containers,
			NodeSelector:  p.config.NodeSelector,
		},
	}

	// Add ConfigMap volumes
	common.AddConfigMapVolumes(&pod.Spec, &pod.Spec.Containers[0], p.config.ConfigMaps)

	// Add Secret volumes
	common.AddSecretVolumes(&pod.Spec, &pod.Spec.Containers[0], p.config.Secrets)

	// Add liveness probe if the process should run continuously
	if p.config.RestartPolicy == "Always" || p.config.RestartPolicy == "OnFailure" {
		pod.Spec.Containers[0].LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"sh", "-c", "pgrep -f '" + p.config.Script.Commands[0] + "' || exit 1",
					},
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    3,
		}
	}
	return pod, nil
}

// periodicStatusUpdater monitors the pod status
func (p *ProcessWorkload) periodicStatusUpdater(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-p.stopCh:
			return
		case <-ticker.C:
			// Use the checkHealth function to check the pod health with retries
			if err := p.checkHealth(ctx); err != nil {
				klog.Warningf("Pod health check failed for %s: %v", p.podName, err)

				// Try to recreate the pod if it's missing and should be active
				p.statusMutex.RLock()
				active := p.status.Active
				p.statusMutex.RUnlock()

				if active && errors.IsNotFound(err) {
					klog.Warningf("Pod %s not found, attempting to recreate", p.podName)
					if createErr := p.createOrUpdatePod(ctx); createErr != nil {
						klog.Errorf("Failed to recreate missing pod %s: %v", p.podName, createErr)
					}
				}
			} else {
				// If health check passes and we previously had a not found error,
				// update the last transition time so we can track how long the pod has been healthy
				p.statusMutex.Lock()
				if strings.Contains(p.status.LastError, "Pod not found") && p.status.Healthy {
					p.status.LastTransition = time.Now()
				}
				p.statusMutex.Unlock()
			}
		}
	}
}

// updatePodStatus updates pod details in the workload status
func (p *ProcessWorkload) updatePodStatus(pod *corev1.Pod) {
	p.updateStatus(func(s *common.WorkloadStatus) {
		// Only update pod details here, not health status
		s.Details["podPhase"] = string(pod.Status.Phase)
		s.Details["podIP"] = pod.Status.PodIP
		s.Details["hostIP"] = pod.Status.HostIP
		s.Details["nodeName"] = pod.Spec.NodeName

		// Add container resource usage if available
		if len(pod.Status.ContainerStatuses) > 0 {
			s.Details["containerReady"] = pod.Status.ContainerStatuses[0].Ready
			s.Details["containerRestartCount"] = pod.Status.ContainerStatuses[0].RestartCount

			// Add state information
			if pod.Status.ContainerStatuses[0].State.Running != nil {
				s.Details["containerStartTime"] = pod.Status.ContainerStatuses[0].State.Running.StartedAt
			}
		}

		// Add more detailed phase information
		if pod.Status.Phase == corev1.PodPending {
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodScheduled && condition.Status == corev1.ConditionFalse {
					s.Details["pendingReason"] = condition.Reason
					s.Details["pendingMessage"] = condition.Message
				}
			}
		}

		// Don't update health status here, that's handled by checkHealth
	})
}

// Helper function to check if a pod is in a terminal state
func isPodTerminated(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed
}

// updateStatus updates the workload status
func (p *ProcessWorkload) updateStatus(updateFn func(*common.WorkloadStatus)) {
	p.statusMutex.Lock()
	defer p.statusMutex.Unlock()
	updateFn(&p.status)
}
