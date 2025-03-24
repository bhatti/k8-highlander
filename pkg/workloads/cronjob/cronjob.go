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

// This file implements the CronJob workload type for k8-highlander.
//
// CronJobWorkload manages individual Kubernetes CronJob resources, handling their
// lifecycle, monitoring their health, and ensuring consistent scheduling behavior.
// It provides a higher-level abstraction that integrates with the leader election
// system to prevent duplicate scheduling across multiple controller instances.
//
// Key features:
// - Creation and management of Kubernetes CronJob resources
// - Controlled suspension and resumption of scheduled jobs
// - Active job tracking and cleanup during shutdown
// - Health monitoring and status reporting
// - Support for ConfigMaps and Secrets
// - Thread-safe operations for concurrent access
//
// This implementation is designed to work with Kubernetes' CronJob controller
// while adding the singleton behavior that k8-highlander provides, ensuring that
// scheduled jobs run exactly once even in multi-node deployments.

package cronjob

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/bhatti/k8-highlander/pkg/monitoring"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// CronJobWorkload implements a Kubernetes CronJob workload.
// It manages the lifecycle of a CronJob, including creation, health monitoring,
// suspension, and cleanup of related jobs and pods.
type CronJobWorkload struct {
	config           common.CronJobConfig
	status           common.WorkloadStatus
	statusMutex      sync.RWMutex
	stopCh           chan struct{}
	client           kubernetes.Interface
	metrics          *monitoring.ControllerMetrics
	monitoringServer *monitoring.MonitoringServer
}

// NewCronJobWorkload creates a new cron job workload
func NewCronJobWorkload(config common.CronJobConfig, metrics *monitoring.ControllerMetrics,
	monitoringServer *monitoring.MonitoringServer) (*CronJobWorkload, error) {
	if err := common.ValidationErrors(config.Validate("")); err != nil {
		return nil, err
	}
	// Set defaults
	if config.RestartPolicy == "" {
		config.RestartPolicy = "OnFailure"
	}

	return &CronJobWorkload{
		config:           config,
		stopCh:           make(chan struct{}),
		metrics:          metrics,
		monitoringServer: monitoringServer,
		status: common.WorkloadStatus{
			Name:    config.Name,
			Type:    common.WorkloadTypeCronJob,
			Active:  false,
			Healthy: false,
			Details: make(map[string]interface{}),
		},
	}, nil
}

// Start creates or updates the CronJob in Kubernetes and begins health monitoring.
// It ensures the CronJob is active (not suspended) and properly configured.
func (c *CronJobWorkload) Start(ctx context.Context, client kubernetes.Interface) error {
	klog.V(2).Infof("Starting cron workload %s in namespace %s", c.config.Name, c.config.Namespace)

	startTime := time.Now()
	c.client = client

	// Create or update the cron job
	err := c.createOrUpdateCronJob(ctx, false)
	duration := time.Since(startTime)

	// Record metrics
	if c.metrics != nil {
		c.metrics.RecordWorkloadOperation(string(c.GetType()), c.GetName(), "startup", duration, err)
		c.metrics.CronJobStatus.WithLabelValues(c.GetName(), c.config.Namespace).Set(1)
	}

	// Update monitoring status
	if c.monitoringServer != nil && err == nil {
		c.monitoringServer.UpdateWorkloadStatus(string(c.GetType()), c.GetName(), c.config.Namespace, true)
	}

	if err != nil {
		klog.Errorf("Failed to start cron workload %s: %v [elapsed: %s]", c.config.Name, err, time.Since(startTime))

		return fmt.Errorf("failed to create or update cron job: %w", err)
	}

	// Start monitoring the cron job health
	go c.monitorHealth(ctx)
	klog.Infof("Successfully started cron workload %s in namespace %s [elapsed: %s %v]",
		c.config.Name, c.config.Namespace, time.Since(startTime), c.monitoringServer.GetLeaderInfo())

	return nil
}

// Stop suspends the CronJob to prevent new jobs from being scheduled and cleans up
// any running jobs that were created by this CronJob. It uses a controlled shutdown
// process with annotations to track workloads during transition.
func (c *CronJobWorkload) Stop(ctx context.Context) error {
	klog.V(2).Infof("Stopping cron workload %s in namespace %s", c.config.Name, c.config.Namespace)

	startTime := time.Now()

	// Use a mutex to safely close the channel only once
	c.statusMutex.Lock()
	if c.stopCh != nil {
		// Check if channel is already closed by trying to read from it
		select {
		case <-c.stopCh:
			// Channel is already closed, create a new one for future use
			klog.V(4).Infof("Stop channel for %s was already closed, creating a new one", c.config.Name)
			c.stopCh = make(chan struct{})
		default:
			// Channel is still open, close it
			close(c.stopCh)
			c.stopCh = nil
		}
	}
	c.statusMutex.Unlock()

	// Mark the CronJob for controlled shutdown by adding an annotation
	cronJob, err := c.client.BatchV1().CronJobs(c.config.Namespace).Get(ctx, c.config.Name, metav1.GetOptions{})
	if err == nil {
		// CronJob exists, add shutdown annotation
		klog.Infof("Marking CronJob %s for controlled shutdown", c.config.Name)
		cronJobCopy := cronJob.DeepCopy()
		if cronJobCopy.Annotations == nil {
			cronJobCopy.Annotations = make(map[string]string)
		}
		cronJobCopy.Annotations["k8-highlander.io/controlled-shutdown"] = "true"
		_, err = c.client.BatchV1().CronJobs(c.config.Namespace).Update(ctx, cronJobCopy, metav1.UpdateOptions{})
		if err != nil {
			klog.Warningf("Failed to mark CronJob %s for controlled shutdown: %v", c.config.Name, err)
		}
	}

	// Suspend the cron job
	err = c.createOrUpdateCronJob(ctx, true)
	duration := time.Since(startTime)

	// Record metrics
	if c.metrics != nil {
		c.metrics.RecordWorkloadOperation(string(c.GetType()), c.GetName(), "shutdown", duration, err)
		c.metrics.CronJobStatus.WithLabelValues(c.GetName(), c.config.Namespace).Set(0)
	}

	// Update monitoring status
	if c.monitoringServer != nil && err == nil {
		c.monitoringServer.UpdateWorkloadStatus(string(c.GetType()), c.GetName(), c.config.Namespace, false)
	}

	if err != nil {
		klog.Errorf("Failed to stop cron workload %s: %v [elapsed: %s]", c.config.Name, err, time.Since(startTime))
		return fmt.Errorf("failed to suspend cron job: %w", err)
	}

	// Clean up any running jobs created by this CronJob
	// The correct selector is "job-name" that starts with the CronJob name
	jobs, err := c.client.BatchV1().Jobs(c.config.Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		klog.Warningf("Error listing jobs for cronjob %s: %v [elapsed: %s]", c.config.Name, err, time.Since(startTime))
	} else {
		// Filter jobs that belong to this CronJob
		for _, job := range jobs.Items {
			// Check if this job was created by our CronJob
			// Jobs created by CronJobs have names that start with the CronJob name
			if strings.HasPrefix(job.Name, c.config.Name+"-") {
				klog.Infof("Deleting job %s for suspended cronjob %s", job.Name, c.config.Name)

				// Add controlled shutdown annotation to the job
				jobCopy := job.DeepCopy()
				if jobCopy.Annotations == nil {
					jobCopy.Annotations = make(map[string]string)
				}
				jobCopy.Annotations["k8-highlander.io/controlled-shutdown"] = "true"
				_, err := c.client.BatchV1().Jobs(c.config.Namespace).Update(ctx, jobCopy, metav1.UpdateOptions{})
				if err != nil {
					klog.Warningf("Failed to mark job %s for controlled shutdown: %v", job.Name, err)
				}

				// Delete the job
				deleteErr := common.RetryWithBackoff(ctx, "delete job", func() error {
					return c.client.BatchV1().Jobs(c.config.Namespace).Delete(ctx, job.Name, metav1.DeleteOptions{})
				})

				if deleteErr != nil && !errors.IsNotFound(deleteErr) {
					klog.Warningf("Error deleting job %s: %v [elapsed: %s]", job.Name, deleteErr, time.Since(startTime))
				}

				// Delete pods created by this job
				pods, err := c.client.CoreV1().Pods(c.config.Namespace).List(ctx, metav1.ListOptions{
					LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
				})
				if err != nil {
					klog.Warningf("Error listing pods for job %s: %v [elapsed: %s]", job.Name, err, time.Since(startTime))
				} else {
					for _, pod := range pods.Items {
						klog.Infof("Deleting pod %s for job %s", pod.Name, job.Name)

						// Delete the pod
						deleteErr := c.client.CoreV1().Pods(c.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
						if deleteErr != nil && !errors.IsNotFound(deleteErr) {
							klog.Warningf("Error deleting pod %s: %v [elapsed: %s]", pod.Name, deleteErr, time.Since(startTime))
						}
					}
				}

				// Wait for pods to be deleted
				waitErr := wait.PollUntilContextTimeout(ctx, 1*time.Second, c.config.TerminationGracePeriod/2+time.Second, true, func(ctx context.Context) (bool, error) {
					// Check if any pods still exist for this job
					pods, err := c.client.CoreV1().Pods(c.config.Namespace).List(ctx, metav1.ListOptions{
						LabelSelector: fmt.Sprintf("job-name=%s", job.Name),
					})

					if err != nil {
						klog.Warningf("Error listing pods for job %s: %v [elapsed: %s]", job.Name, err, time.Since(startTime))
						return false, nil
					}

					if len(pods.Items) == 0 {
						return true, nil // All pods are gone
					}

					// Force delete any pods that are stuck
					for _, pod := range pods.Items {
						if pod.DeletionTimestamp != nil {
							terminatingTime := time.Since(pod.DeletionTimestamp.Time)
							if terminatingTime > c.config.TerminationGracePeriod/2-time.Second {
								klog.Warningf("Pod %s stuck in Terminating state for %v, force deleting [elapsed: %s]",
									pod.Name, terminatingTime, time.Since(startTime))

								common.ForceDeletePod(ctx, c.client, c.config.Namespace, pod.Name)
							}
						} else {
							// Pod exists but doesn't have a deletion timestamp, try to delete it
							klog.Warningf("Pod %s exists without deletion timestamp, deleting [elapsed: %s]", pod.Name, time.Since(startTime))
							deleteErr := c.client.CoreV1().Pods(c.config.Namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{})
							if deleteErr != nil && !errors.IsNotFound(deleteErr) {
								klog.Errorf("Error deleting pod %s: %v [elapsed: %s]", pod.Name, deleteErr, time.Since(startTime))
							}
						}
					}

					return false, nil // Some pods still exist, keep waiting
				})

				if waitErr != nil {
					klog.Warningf("Timed out waiting for job pods to be deleted: %v [elapsed: %s]", waitErr, time.Since(startTime))
				}
			}
		}
	}

	klog.Infof("Successfully stopped cron workload %s in namespace %s [elapsed: %s]", c.config.Name, c.config.Namespace, time.Since(startTime))

	return nil
}

func (c *CronJobWorkload) SetMonitoring(metrics *monitoring.ControllerMetrics, monitoringServer *monitoring.MonitoringServer) {
	c.metrics = metrics
	c.monitoringServer = monitoringServer
}

// GetStatus returns the current status of the workload
func (c *CronJobWorkload) GetStatus() common.WorkloadStatus {
	c.statusMutex.RLock()
	defer c.statusMutex.RUnlock()
	return c.status
}

// GetName returns the name of the workload
func (c *CronJobWorkload) GetName() string {
	return c.config.Name
}

// GetType returns the type of the workload
func (c *CronJobWorkload) GetType() common.WorkloadType {
	return common.WorkloadTypeCronJob
}

// createOrUpdateCronJob creates or updates the cron job
func (c *CronJobWorkload) createOrUpdateCronJob(ctx context.Context, suspend bool) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Get the latest version of the CronJob
		existing, err := c.client.BatchV1().CronJobs(c.config.Namespace).Get(ctx, c.config.Name, metav1.GetOptions{})

		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to check if cron job exists: %w", err)
		}

		if errors.IsNotFound(err) {
			// Create new cron job
			cronJob := c.buildCronJob(suspend)
			_, err = c.client.BatchV1().CronJobs(c.config.Namespace).Create(ctx, cronJob, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create cron job: %w", err)
			}

			klog.Infof("Created cron job %s in namespace %s, %s",
				c.config.Name, c.config.Namespace, common.ContainersSummary(cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers))
		} else {
			// Update existing cron job
			// Only update the suspend field to minimize conflicts
			suspendValue := suspend
			existing.Spec.Suspend = &suspendValue

			_, err = c.client.BatchV1().CronJobs(c.config.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
			if err != nil {
				return fmt.Errorf("failed to update cron job: %w", err)
			}

			klog.Infof("Updated cron job %s in namespace %s (suspend=%v) [%v]",
				c.config.Name, c.config.Namespace, suspend, c.monitoringServer.GetLeaderInfo())
		}

		// Update status
		c.updateStatus(func(s *common.WorkloadStatus) {
			s.Active = !suspend
			s.Details["suspend"] = suspend
			s.Details["schedule"] = c.config.Schedule
			s.LastTransition = time.Now()
		})

		return nil
	})
}

// monitorHealth monitors the health of the cron job
func (c *CronJobWorkload) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			if err := c.checkHealth(ctx); err != nil {
				klog.Errorf("CronJob health check failed: %v", err)

				c.updateStatus(func(s *common.WorkloadStatus) {
					s.Healthy = false
					s.LastError = err.Error()
				})
			} else {
				c.updateStatus(func(s *common.WorkloadStatus) {
					s.Healthy = true
					s.LastError = ""
				})
			}
		}
	}
}

// checkHealth checks the health of the cron job
func (c *CronJobWorkload) checkHealth(ctx context.Context) error {
	cronJob, err := c.client.BatchV1().CronJobs(c.config.Namespace).Get(ctx, c.config.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			// Update metrics
			if c.metrics != nil {
				c.metrics.CronJobStatus.WithLabelValues(c.GetName(), c.config.Namespace).Set(0)
			}

			// Update monitoring status
			if c.monitoringServer != nil {
				c.monitoringServer.UpdateWorkloadStatus(string(c.GetType()), c.GetName(), c.config.Namespace, false)
			}
			return fmt.Errorf("cron job not found: %w", err)
		}
		return err
	}

	// Update status with details
	c.updateStatus(func(s *common.WorkloadStatus) {
		s.Details["lastScheduleTime"] = cronJob.Status.LastScheduleTime
		s.Details["activeJobs"] = len(cronJob.Status.Active)
		s.Active = !*cronJob.Spec.Suspend
		s.Healthy = true

		// Check if there are any active jobs
		if len(cronJob.Status.Active) > 0 {
			s.Details["activeJobNames"] = []string{}
			for _, job := range cronJob.Status.Active {
				s.Details["activeJobNames"] = append(s.Details["activeJobNames"].([]string), job.Name)
			}
		}

		// Update metrics
		if c.metrics != nil {
			c.metrics.CronJobStatus.WithLabelValues(c.GetName(), c.config.Namespace).Set(s.ActiveHealthyFloat())
		}

		// Update monitoring status
		if c.monitoringServer != nil {
			c.monitoringServer.UpdateWorkloadStatus(string(c.GetType()), c.GetName(), c.config.Namespace, s.Active)
		}
	})

	return nil
}

// buildCronJob builds a Kubernetes CronJob from the config
func (c *CronJobWorkload) buildCronJob(suspend bool) *batchv1.CronJob {
	labels, containers := c.config.BuildContainers(c.monitoringServer.GetLeaderInfo())
	// Create the cron job
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:        c.config.Name,
			Namespace:   c.config.Namespace,
			Labels:      labels,
			Annotations: c.config.Annotations,
		},
		Spec: batchv1.CronJobSpec{
			Schedule: c.config.Schedule,
			Suspend:  &suspend,
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      labels,
					Annotations: c.config.Annotations,
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels:      labels,
							Annotations: c.config.Annotations,
						},
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicy(c.config.RestartPolicy),
							Containers:    containers,
							NodeSelector:  c.config.NodeSelector,
						},
					},
				},
			},
		},
	}

	// Add ConfigMap volumes
	common.AddConfigMapVolumes(&cronJob.Spec.JobTemplate.Spec.Template.Spec, &cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0], c.config.ConfigMaps)

	// Add Secret volumes
	common.AddSecretVolumes(&cronJob.Spec.JobTemplate.Spec.Template.Spec, &cronJob.Spec.JobTemplate.Spec.Template.Spec.Containers[0], c.config.Secrets)

	return cronJob
}

// updateStatus updates the workload status
func (c *CronJobWorkload) updateStatus(updateFn func(*common.WorkloadStatus)) {
	c.statusMutex.Lock()
	defer c.statusMutex.Unlock()
	updateFn(&c.status)
}

// MonitorCronJob TODO
func (c *CronJobWorkload) MonitorCronJob(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			cronJob, err := c.client.BatchV1().CronJobs(c.config.Namespace).Get(ctx, c.config.Name, metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					// CronJob doesn't exist, recreate it if we're active
					c.statusMutex.RLock()
					active := c.status.Active
					c.statusMutex.RUnlock()

					if active {
						klog.Warningf("CronJob %s not found, recreating", c.config.Name)
						if err := c.createOrUpdateCronJob(ctx, false); err != nil {
							klog.Errorf("Failed to recreate CronJob: %v", err)
						}
					}
				} else {
					klog.Errorf("Failed to get CronJob %s: %v", c.config.Name, err)
				}
				continue
			}

			// Check if the CronJob was modified externally
			if cronJob.Spec.Suspend != nil {
				shouldBeSuspended := !c.status.Active
				isSuspended := *cronJob.Spec.Suspend

				if shouldBeSuspended != isSuspended {
					klog.Warningf("CronJob %s suspension state changed externally, fixing", c.config.Name)
					if err := c.createOrUpdateCronJob(ctx, shouldBeSuspended); err != nil {
						klog.Errorf("Failed to update CronJob suspension state: %v", err)
					}
				}
			}
		}
	}
}
