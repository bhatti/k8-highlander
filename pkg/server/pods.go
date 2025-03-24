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

// This file extends the server package with Kubernetes pod-related API endpoints.
// It provides pod information for the dashboard and enables monitoring of managed
// workloads through a simplified API.
//
// The implementation:
// - Exposes pod information via RESTful endpoints
// - Extracts leader and cluster information from pod annotations/labels
// - Formats pod information for dashboard consumption
// - Handles Kubernetes API interactions

package server

import (
	"context"
	"encoding/json"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"net/http"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

// PodInfo represents simplified pod information for the dashboard
type PodInfo struct {
	Name         string            `json:"name"`
	Namespace    string            `json:"namespace"`
	Status       string            `json:"status"`
	Node         string            `json:"node"`
	Age          string            `json:"age"`
	CreatedAt    time.Time         `json:"createdAt"`
	ControllerID string            `json:"controllerId"`
	ClusterName  string            `json:"clusterName"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
}

// SetupPodEndpoints adds Kubernetes pod-related API endpoints
func (s *Server) SetupPodEndpoints(mux *http.ServeMux, client kubernetes.Interface, namespace string) {
	mux.HandleFunc("/api/pods", func(w http.ResponseWriter, r *http.Request) {
		pods, err := listPods(r.Context(), client, namespace)
		if err != nil {
			klog.Errorf("Error listing pods: %v", err)
			http.Error(w, "Failed to list pods: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status": "success",
			"data":   pods,
		})
	})

	//klog.Infof("Pod API endpoints configured")
}

// listPods retrieves pods from Kubernetes and formats them for the dashboard
func listPods(ctx context.Context, client kubernetes.Interface, namespace string) ([]PodInfo, error) {
	if client == nil {
		return nil, nil
	}

	// List pods in the namespace
	podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	// Convert to simplified format
	var pods []PodInfo
	for _, pod := range podList.Items {
		age := time.Since(pod.CreationTimestamp.Time)

		// Extract leader ID and cluster name from annotations or labels
		leaderID := ""
		clusterName := ""

		// Check annotations first
		if pod.Annotations != nil {
			leaderID = pod.Annotations["k8-highlander.io/leader-id"]
			clusterName = pod.Annotations["k8-highlander.io/cluster-name"]
		}

		// If not found in annotations, check labels
		if leaderID == "" && pod.Labels != nil {
			leaderID = pod.Labels["controller-id"]
		}

		if clusterName == "" && pod.Labels != nil {
			clusterName = pod.Labels["cluster-name"]
		}

		pods = append(pods, PodInfo{
			Name:         pod.Name,
			Namespace:    pod.Namespace,
			Status:       string(pod.Status.Phase),
			Node:         pod.Spec.NodeName,
			Age:          formatDuration(age),
			CreatedAt:    pod.CreationTimestamp.Time,
			ControllerID: leaderID,
			ClusterName:  clusterName,
			Labels:       pod.Labels,
			Annotations:  pod.Annotations,
		})
	}

	return pods, nil
}

// formatDuration formats a duration in a human-readable format
func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)

	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour

	hours := d / time.Hour
	d -= hours * time.Hour

	minutes := d / time.Minute
	d -= minutes * time.Minute

	seconds := d / time.Second

	if days > 0 {
		return fmt.Sprintf("%dd%dh", days, hours)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh%dm", hours, minutes)
	}
	if minutes > 0 {
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("%ds", seconds)
}
