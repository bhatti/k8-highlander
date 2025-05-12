package crd

import (
	"context"
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"time"
)

// CRDClient manages interactions with the workload CRDs
type CRDClient struct {
	dynamicClient dynamic.Interface
}

// NewCRDClient creates a new CRD client
func NewCRDClient(dynamicClient dynamic.Interface) *CRDClient {
	return &CRDClient{
		dynamicClient: dynamicClient,
	}
}

// FetchWorkloadProcessConfig fetches a WorkloadProcess CRD and converts it to ProcessConfig
func (c *CRDClient) FetchWorkloadProcessConfig(ctx context.Context, ref *common.WorkloadCRDReference) (*common.ProcessConfig, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Define the GVR (Group, Version, Resource) for WorkloadProcess
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ProcessesResource,
	}

	// Use namespace from reference or default to current namespace
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Fetch the CRD
	unstructuredObj, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WorkloadProcess: %w", err)
	}

	// Convert the unstructured object to ProcessConfig
	return convertToProcessConfig(unstructuredObj, ref.Name, namespace)
}

// FetchWorkloadServiceConfig fetches a WorkloadService CRD and converts it to ServiceConfig
func (c *CRDClient) FetchWorkloadServiceConfig(ctx context.Context, ref *common.WorkloadCRDReference) (*common.ServiceConfig, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Define the GVR for WorkloadService
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.ServicesResource,
	}

	// Use namespace from reference or default to current namespace
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Fetch the CRD
	unstructuredObj, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WorkloadService: %w", err)
	}

	// Convert the unstructured object to ServiceConfig
	return convertToServiceConfig(unstructuredObj, ref.Name, namespace)
}

// FetchWorkloadCronJobConfig fetches a WorkloadCronJob CRD and converts it to CronJobConfig
func (c *CRDClient) FetchWorkloadCronJobConfig(ctx context.Context, ref *common.WorkloadCRDReference) (*common.CronJobConfig, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Define the GVR for WorkloadCronJob
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.CronjobsResource,
	}

	// Use namespace from reference or default to current namespace
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Fetch the CRD
	unstructuredObj, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WorkloadCronJob: %w", err)
	}

	// Convert the unstructured object to CronJobConfig
	return convertToCronJobConfig(unstructuredObj, ref.Name, namespace)
}

// FetchWorkloadPersistentConfig fetches a WorkloadPersistent CRD and converts it to PersistentConfig
func (c *CRDClient) FetchWorkloadPersistentConfig(ctx context.Context, ref *common.WorkloadCRDReference) (*common.PersistentConfig, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Define the GVR for WorkloadPersistent
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: common.PersistentsResource,
	}

	// Use namespace from reference or default to current namespace
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Fetch the CRD
	unstructuredObj, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get WorkloadPersistent: %w", err)
	}

	// Convert the unstructured object to PersistentConfig
	return convertToPersistentConfig(unstructuredObj, ref.Name, namespace)
}

// Helper functions to convert unstructured objects to config types

// convertToProcessConfig converts an unstructured WorkloadProcess to ProcessConfig
func convertToProcessConfig(obj *unstructured.Unstructured, name, namespace string) (*common.ProcessConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadProcess: %w", err)
	}

	// Create a basic ProcessConfig
	config := &common.ProcessConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Get image
	if image, found, _ := unstructured.NestedString(spec, "image"); found {
		config.Image = image
	}

	// Get script
	if scriptMap, found, _ := unstructured.NestedMap(spec, "script"); found {
		script := &common.ScriptConfig{}

		// Get commands
		if commandsUntyped, found, _ := unstructured.NestedSlice(scriptMap, "commands"); found {
			for _, cmd := range commandsUntyped {
				if cmdStr, ok := cmd.(string); ok {
					script.Commands = append(script.Commands, cmdStr)
				}
			}
		}

		// Get shell
		if shell, found, _ := unstructured.NestedString(scriptMap, "shell"); found {
			script.Shell = shell
		}

		config.Script = script
	}

	// Get environment variables
	if envMap, found, _ := unstructured.NestedMap(spec, "env"); found {
		config.Env = make(map[string]string)
		for k, v := range envMap {
			if strVal, ok := v.(string); ok {
				config.Env[k] = strVal
			}
		}
	}

	// Get resources
	if resourcesMap, found, _ := unstructured.NestedMap(spec, "resources"); found {
		if cpuRequest, found, _ := unstructured.NestedString(resourcesMap, "cpuRequest"); found {
			config.Resources.CPURequest = cpuRequest
		}
		if memoryRequest, found, _ := unstructured.NestedString(resourcesMap, "memoryRequest"); found {
			config.Resources.MemoryRequest = memoryRequest
		}
		if cpuLimit, found, _ := unstructured.NestedString(resourcesMap, "cpuLimit"); found {
			config.Resources.CPULimit = cpuLimit
		}
		if memoryLimit, found, _ := unstructured.NestedString(resourcesMap, "memoryLimit"); found {
			config.Resources.MemoryLimit = memoryLimit
		}
	}

	// Get restart policy
	if restartPolicy, found, _ := unstructured.NestedString(spec, "restartPolicy"); found {
		config.RestartPolicy = restartPolicy
	}

	// Get max restarts
	if maxRestarts, found, _ := unstructured.NestedInt64(spec, "maxRestarts"); found {
		config.MaxRestarts = int(maxRestarts)
	}

	// Get grace period
	if gracePeriod, found, _ := unstructured.NestedString(spec, "gracePeriod"); found {
		if duration, err := time.ParseDuration(gracePeriod); err == nil {
			config.GracePeriod = duration
		}
	}
	config.WorkloadCRDRef = &common.WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	return config, nil
}

// convertToServiceConfig converts an unstructured WorkloadService to ServiceConfig
func convertToServiceConfig(obj *unstructured.Unstructured, name, namespace string) (*common.ServiceConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadService: %w", err)
	}

	// Create a basic ServiceConfig
	config := &common.ServiceConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Get replicas
	if replicas, found, _ := unstructured.NestedInt64(spec, "replicas"); found {
		config.Replicas = int32(replicas)
	}

	// Get image
	if image, found, _ := unstructured.NestedString(spec, "image"); found {
		config.Image = image
	}

	// Get script
	if scriptMap, found, _ := unstructured.NestedMap(spec, "script"); found {
		script := &common.ScriptConfig{}

		// Get commands
		if commandsUntyped, found, _ := unstructured.NestedSlice(scriptMap, "commands"); found {
			for _, cmd := range commandsUntyped {
				if cmdStr, ok := cmd.(string); ok {
					script.Commands = append(script.Commands, cmdStr)
				}
			}
		}

		// Get shell
		if shell, found, _ := unstructured.NestedString(scriptMap, "shell"); found {
			script.Shell = shell
		}

		config.Script = script
	}

	// Get environment variables
	if envMap, found, _ := unstructured.NestedMap(spec, "env"); found {
		config.Env = make(map[string]string)
		for k, v := range envMap {
			if strVal, ok := v.(string); ok {
				config.Env[k] = strVal
			}
		}
	}

	// Get resources
	if resourcesMap, found, _ := unstructured.NestedMap(spec, "resources"); found {
		if cpuRequest, found, _ := unstructured.NestedString(resourcesMap, "cpuRequest"); found {
			config.Resources.CPURequest = cpuRequest
		}
		if memoryRequest, found, _ := unstructured.NestedString(resourcesMap, "memoryRequest"); found {
			config.Resources.MemoryRequest = memoryRequest
		}
		if cpuLimit, found, _ := unstructured.NestedString(resourcesMap, "cpuLimit"); found {
			config.Resources.CPULimit = cpuLimit
		}
		if memoryLimit, found, _ := unstructured.NestedString(resourcesMap, "memoryLimit"); found {
			config.Resources.MemoryLimit = memoryLimit
		}
	}

	// Get ports
	if portsUntyped, found, _ := unstructured.NestedSlice(spec, "ports"); found {
		for _, portUntyped := range portsUntyped {
			if portMap, ok := portUntyped.(map[string]interface{}); ok {
				port := common.PortConfig{}

				if name, found, _ := unstructured.NestedString(portMap, "name"); found {
					port.Name = name
				}

				if containerPort, found, _ := unstructured.NestedInt64(portMap, "containerPort"); found {
					port.ContainerPort = int32(containerPort)
				}

				if servicePort, found, _ := unstructured.NestedInt64(portMap, "servicePort"); found {
					port.ServicePort = int32(servicePort)
				}

				config.Ports = append(config.Ports, port)
			}
		}
	}

	// Get health check path
	if healthCheckPath, found, _ := unstructured.NestedString(spec, "healthCheckPath"); found {
		config.HealthCheckPath = healthCheckPath
	}

	// Get health check port
	if healthCheckPort, found, _ := unstructured.NestedInt64(spec, "healthCheckPort"); found {
		config.HealthCheckPort = int32(healthCheckPort)
	}

	// Get readiness timeout
	if readinessTimeout, found, _ := unstructured.NestedString(spec, "readinessTimeout"); found {
		if duration, err := time.ParseDuration(readinessTimeout); err == nil {
			config.ReadinessTimeout = duration
		}
	}

	return config, nil
}

// convertToCronJobConfig converts an unstructured WorkloadCronJob to CronJobConfig
func convertToCronJobConfig(obj *unstructured.Unstructured, name, namespace string) (*common.CronJobConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadCronJob: %w", err)
	}

	// Create a basic CronJobConfig
	config := &common.CronJobConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Get schedule
	if schedule, found, _ := unstructured.NestedString(spec, "schedule"); found {
		config.Schedule = schedule
	}

	// Get image
	if image, found, _ := unstructured.NestedString(spec, "image"); found {
		config.Image = image
	}

	// Get script
	if scriptMap, found, _ := unstructured.NestedMap(spec, "script"); found {
		script := &common.ScriptConfig{}

		// Get commands
		if commandsUntyped, found, _ := unstructured.NestedSlice(scriptMap, "commands"); found {
			for _, cmd := range commandsUntyped {
				if cmdStr, ok := cmd.(string); ok {
					script.Commands = append(script.Commands, cmdStr)
				}
			}
		}

		// Get shell
		if shell, found, _ := unstructured.NestedString(scriptMap, "shell"); found {
			script.Shell = shell
		}

		config.Script = script
	}

	// Get environment variables
	if envMap, found, _ := unstructured.NestedMap(spec, "env"); found {
		config.Env = make(map[string]string)
		for k, v := range envMap {
			if strVal, ok := v.(string); ok {
				config.Env[k] = strVal
			}
		}
	}

	// Get resources
	if resourcesMap, found, _ := unstructured.NestedMap(spec, "resources"); found {
		if cpuRequest, found, _ := unstructured.NestedString(resourcesMap, "cpuRequest"); found {
			config.Resources.CPURequest = cpuRequest
		}
		if memoryRequest, found, _ := unstructured.NestedString(resourcesMap, "memoryRequest"); found {
			config.Resources.MemoryRequest = memoryRequest
		}
		if cpuLimit, found, _ := unstructured.NestedString(resourcesMap, "cpuLimit"); found {
			config.Resources.CPULimit = cpuLimit
		}
		if memoryLimit, found, _ := unstructured.NestedString(resourcesMap, "memoryLimit"); found {
			config.Resources.MemoryLimit = memoryLimit
		}
	}

	// Get restart policy
	if restartPolicy, found, _ := unstructured.NestedString(spec, "restartPolicy"); found {
		config.RestartPolicy = restartPolicy
	}

	return config, nil
}

// convertToPersistentConfig converts an unstructured WorkloadPersistent to PersistentConfig
func convertToPersistentConfig(obj *unstructured.Unstructured, name, namespace string) (*common.PersistentConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadPersistent: %w", err)
	}

	// Create a basic PersistentConfig
	config := &common.PersistentConfig{
		BaseWorkloadConfig: common.BaseWorkloadConfig{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Get replicas
	if replicas, found, _ := unstructured.NestedInt64(spec, "replicas"); found {
		config.Replicas = int32(replicas)
	}

	// Get image
	if image, found, _ := unstructured.NestedString(spec, "image"); found {
		config.Image = image
	}

	// Get script
	if scriptMap, found, _ := unstructured.NestedMap(spec, "script"); found {
		script := &common.ScriptConfig{}

		// Get commands
		if commandsUntyped, found, _ := unstructured.NestedSlice(scriptMap, "commands"); found {
			for _, cmd := range commandsUntyped {
				if cmdStr, ok := cmd.(string); ok {
					script.Commands = append(script.Commands, cmdStr)
				}
			}
		}

		// Get shell
		if shell, found, _ := unstructured.NestedString(scriptMap, "shell"); found {
			script.Shell = shell
		}

		config.Script = script
	}

	// Get environment variables
	if envMap, found, _ := unstructured.NestedMap(spec, "env"); found {
		config.Env = make(map[string]string)
		for k, v := range envMap {
			if strVal, ok := v.(string); ok {
				config.Env[k] = strVal
			}
		}
	}

	// Get resources
	if resourcesMap, found, _ := unstructured.NestedMap(spec, "resources"); found {
		if cpuRequest, found, _ := unstructured.NestedString(resourcesMap, "cpuRequest"); found {
			config.Resources.CPURequest = cpuRequest
		}
		if memoryRequest, found, _ := unstructured.NestedString(resourcesMap, "memoryRequest"); found {
			config.Resources.MemoryRequest = memoryRequest
		}
		if cpuLimit, found, _ := unstructured.NestedString(resourcesMap, "cpuLimit"); found {
			config.Resources.CPULimit = cpuLimit
		}
		if memoryLimit, found, _ := unstructured.NestedString(resourcesMap, "memoryLimit"); found {
			config.Resources.MemoryLimit = memoryLimit
		}
	}

	// Get ports
	if portsUntyped, found, _ := unstructured.NestedSlice(spec, "ports"); found {
		for _, portUntyped := range portsUntyped {
			if portMap, ok := portUntyped.(map[string]interface{}); ok {
				port := common.PortConfig{}

				if name, found, _ := unstructured.NestedString(portMap, "name"); found {
					port.Name = name
				}

				if containerPort, found, _ := unstructured.NestedInt64(portMap, "containerPort"); found {
					port.ContainerPort = int32(containerPort)
				}

				if servicePort, found, _ := unstructured.NestedInt64(portMap, "servicePort"); found {
					port.ServicePort = int32(servicePort)
				}

				config.Ports = append(config.Ports, port)
			}
		}
	}

	// Get persistent volumes
	if volumesUntyped, found, _ := unstructured.NestedSlice(spec, "persistentVolumes"); found {
		for _, volumeUntyped := range volumesUntyped {
			if volumeMap, ok := volumeUntyped.(map[string]interface{}); ok {
				volume := common.VolumeConfig{}

				if name, found, _ := unstructured.NestedString(volumeMap, "name"); found {
					volume.Name = name
				}

				if mountPath, found, _ := unstructured.NestedString(volumeMap, "mountPath"); found {
					volume.MountPath = mountPath
				}

				if storageClassName, found, _ := unstructured.NestedString(volumeMap, "storageClassName"); found {
					volume.StorageClassName = storageClassName
				}

				if size, found, _ := unstructured.NestedString(volumeMap, "size"); found {
					volume.Size = size
				}

				config.PersistentVolumes = append(config.PersistentVolumes, volume)
			}
		}
	}

	// Get service name
	if serviceName, found, _ := unstructured.NestedString(spec, "serviceName"); found {
		config.ServiceName = serviceName
	}

	// Get health check path
	if healthCheckPath, found, _ := unstructured.NestedString(spec, "healthCheckPath"); found {
		config.HealthCheckPath = healthCheckPath
	}

	// Get health check port
	if healthCheckPort, found, _ := unstructured.NestedInt64(spec, "healthCheckPort"); found {
		config.HealthCheckPort = int32(healthCheckPort)
	}

	// Get readiness timeout
	if readinessTimeout, found, _ := unstructured.NestedString(spec, "readinessTimeout"); found {
		if duration, err := time.ParseDuration(readinessTimeout); err == nil {
			config.ReadinessTimeout = duration
		}
	}

	return config, nil
}

// FetchWorkloadConfig fetches a workload configuration from a CRD reference
func (c *CRDClient) FetchWorkloadConfig(ctx context.Context, ref *common.WorkloadCRDReference) (interface{}, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Determine which type of workload to fetch based on the Kind
	switch ref.Kind {
	case "WorkloadProcess":
		return c.FetchWorkloadProcessConfig(ctx, ref)
	case "WorkloadService":
		return c.FetchWorkloadServiceConfig(ctx, ref)
	case "WorkloadCronJob":
		return c.FetchWorkloadCronJobConfig(ctx, ref)
	case "WorkloadPersistent":
		return c.FetchWorkloadPersistentConfig(ctx, ref)
	default:
		return nil, fmt.Errorf("unsupported workload CRD kind: %s", ref.Kind)
	}
}

// UpdateWorkloadStatus updates the status of a workload CRD
func (c *CRDClient) UpdateWorkloadStatus(ctx context.Context, ref *common.WorkloadCRDReference, status *common.WorkloadStatus) error {
	if ref == nil || status == nil {
		return fmt.Errorf("workload CRD reference or status is nil")
	}

	// Determine resource type from Kind
	var resource string
	switch ref.Kind {
	case "WorkloadProcess":
		resource = common.ProcessesResource
	case "WorkloadService":
		resource = common.ServicesResource
	case "WorkloadCronJob":
		resource = common.CronjobsResource
	case "WorkloadPersistent":
		resource = common.PersistentsResource
	default:
		return fmt.Errorf("unsupported workload CRD kind: %s", ref.Kind)
	}

	// Define the GVR for the resource
	gvr := schema.GroupVersionResource{
		Group:    common.GROUP,
		Version:  common.MajorVersion,
		Resource: resource,
	}

	// Use namespace from reference or default
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Get the current object
	unstructuredObj, err := c.dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get %s: %w", ref.Kind, err)
	}

	// Create a copy to update
	objCopy := unstructuredObj.DeepCopy()

	// Create a status object
	statusMap := map[string]interface{}{
		"active":  status.Active,
		"healthy": status.Healthy,
	}

	if status.LastError != "" {
		statusMap["lastError"] = status.LastError
	}

	if !status.LastTransition.IsZero() {
		statusMap["lastTransition"] = status.LastTransition.Format(time.RFC3339)
	}

	// Update the status
	if err := unstructured.SetNestedMap(objCopy.Object, statusMap, "status"); err != nil {
		return fmt.Errorf("failed to set status: %w", err)
	}

	// Update the object
	_, err = c.dynamicClient.Resource(gvr).Namespace(namespace).UpdateStatus(ctx, objCopy, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to update status for %s: %w", ref.Kind, err)
	}

	klog.Infof("Updated status for %s %s/%s", ref.Kind, namespace, ref.Name)
	return nil
}
