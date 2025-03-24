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

// Package common provides Kubernetes client utilities for k8-highlander.
//
// This file implements utility functions for creating and working with Kubernetes
// clients. It provides optimized client initialization and retry mechanisms to
// handle transient API errors when interacting with the Kubernetes API server.
//
// Key features:
// - Client creation with proper configuration
// - Rate limit avoidance with optimized QPS and burst settings
// - Exponential backoff retry logic for API operations
// - Smart error categorization to avoid unnecessary retries
//
// These utilities improve reliability when working with Kubernetes resources
// by handling common failure scenarios and implementing best practices for
// client configuration and error handling.

package common

import (
	"context"
	"fmt"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	"time"
)

// CreateKubernetesClient creates a properly configured Kubernetes client from a
// kubeconfig file. It applies performance optimizations like increased QPS and
// burst limits to avoid rate limiting in high-throughput scenarios.
//
// Parameters:
//   - kubeconfigPath: Path to the kubeconfig file. If empty, uses in-cluster config.
//
// Returns:
//   - kubernetes.Interface: The configured Kubernetes client
//   - error: Any error encountered during client creation
func CreateKubernetesClient(kubeconfigPath string) (kubernetes.Interface, error) {
	// Build config from kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("error building kubeconfig: %w", err)
	}

	// Increase QPS and Burst limits to avoid rate limiting during tests
	config.QPS = 100
	config.Burst = 100

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("error creating kubernetes client: %w", err)
	}

	return clientset, nil
}

// RetryWithBackoff executes an operation with exponential backoff retry logic.
// It intelligently handles different types of errors, avoiding retries for
// expected conditions like "not found" or "already exists" errors.
//
// Parameters:
//   - ctx: Context for cancellation
//   - operation: Name of the operation (for logging)
//   - fn: The function to execute with retries
//
// Returns:
//   - error: The final error after all retry attempts, or nil on success
func RetryWithBackoff(ctx context.Context, operation string, fn func() error) error {
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 100 * time.Millisecond,
		Factor:   2.0,
		Jitter:   0.1,
	}

	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := fn()
		if err == nil {
			return true, nil
		}

		if errors.IsNotFound(err) || errors.IsAlreadyExists(err) {
			// These errors are expected in some cases and shouldn't be retried
			return true, err
		}

		klog.V(4).Infof("Retrying %s due to error: %v", operation, err)
		return false, nil
	})
}

// ForceDeletePod attempts to force delete a pod, first by removing finalizers, then using gracePeriodSeconds=0
func ForceDeletePod(ctx context.Context, client kubernetes.Interface, namespace, podName string) error {
	klog.Warningf("Force deleting pod %s/%s", namespace, podName)

	// First try to remove finalizers if present
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return nil // Pod is already gone
		}
		if err != nil {
			return err
		}

		// If pod has finalizers, remove them
		if len(pod.Finalizers) > 0 {
			klog.Infof("Removing finalizers from pod %s/%s: %v", namespace, podName, pod.Finalizers)
			podCopy := pod.DeepCopy()
			podCopy.Finalizers = nil
			_, err = client.CoreV1().Pods(namespace).Update(ctx, podCopy, metav1.UpdateOptions{})
			return err
		}
		return nil
	})

	if err != nil && !errors.IsNotFound(err) {
		klog.Warningf("Failed to remove finalizers from pod %s/%s: %v", namespace, podName, err)
		// Continue with force delete anyway
	}

	// Now force delete with gracePeriodSeconds=0
	zero := int64(0)
	err = client.CoreV1().Pods(namespace).Delete(ctx, podName, metav1.DeleteOptions{
		GracePeriodSeconds: &zero,
		PropagationPolicy:  &[]metav1.DeletionPropagation{metav1.DeletePropagationBackground}[0],
	})

	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to force delete pod %s/%s: %w", namespace, podName, err)
	}

	return nil
}
