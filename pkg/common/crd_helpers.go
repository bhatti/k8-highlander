package common

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

// CreateProcessCRD creates a WorkloadProcess custom resource
func CreateProcessCRD(ctx context.Context, dynamicClient dynamic.Interface, name, namespace, image string, commands []string,
	shell string, env map[string]string, resources ResourceConfig, restartPolicy string, maxRestarts int,
	gracePeriod string) error {

	// Create a typed object first
	processConfig := &struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			Image  string `json:"image"`
			Script struct {
				Commands []string `json:"commands"`
				Shell    string   `json:"shell"`
			} `json:"script"`
			Env       map[string]string `json:"env,omitempty"`
			Resources struct {
				CPURequest    string `json:"cpuRequest,omitempty"`
				MemoryRequest string `json:"memoryRequest,omitempty"`
				CPULimit      string `json:"cpuLimit,omitempty"`
				MemoryLimit   string `json:"memoryLimit,omitempty"`
			} `json:"resources,omitempty"`
			RestartPolicy string `json:"restartPolicy,omitempty"`
			MaxRestarts   int    `json:"maxRestarts,omitempty"`
			GracePeriod   string `json:"gracePeriod,omitempty"`
		} `json:"spec"`
	}{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fmt.Sprintf("%s/%s", GROUP, MajorVersion),
			Kind:       "WorkloadProcess",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Set spec fields
	processConfig.Spec.Image = image
	processConfig.Spec.Script.Commands = commands
	processConfig.Spec.Script.Shell = shell
	processConfig.Spec.Env = env
	processConfig.Spec.Resources.CPURequest = resources.CPURequest
	processConfig.Spec.Resources.MemoryRequest = resources.MemoryRequest
	processConfig.Spec.Resources.CPULimit = resources.CPULimit
	processConfig.Spec.Resources.MemoryLimit = resources.MemoryLimit
	processConfig.Spec.RestartPolicy = restartPolicy
	processConfig.Spec.MaxRestarts = maxRestarts
	processConfig.Spec.GracePeriod = gracePeriod

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(processConfig)
	if err != nil {
		return fmt.Errorf("failed to convert ProcessConfig to unstructured: %w", err)
	}

	// Create the unstructured object
	obj := &unstructured.Unstructured{Object: unstructuredObj}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: ProcessesResource,
	}

	// Create the resource
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create WorkloadProcess '%s/%s': %w", namespace, name, err)
	}

	klog.Infof("Successfully created WorkloadProcess '%s/%s'", namespace, name)
	return nil
}

// CreateServiceCRD creates a WorkloadService custom resource
func CreateServiceCRD(ctx context.Context, dynamicClient dynamic.Interface, name, namespace, image string,
	commands []string, shell string, replicas int32, ports []PortConfig, env map[string]string,
	resources ResourceConfig, healthCheckPath string, healthCheckPort int32) error {

	// Create a typed object first
	serviceConfig := &struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			Image  string `json:"image"`
			Script struct {
				Commands []string `json:"commands"`
				Shell    string   `json:"shell"`
			} `json:"script"`
			Replicas int32 `json:"replicas"`
			Ports    []struct {
				Name          string `json:"name"`
				ContainerPort int32  `json:"containerPort"`
				ServicePort   int32  `json:"servicePort"`
			} `json:"ports,omitempty"`
			Env       map[string]string `json:"env,omitempty"`
			Resources struct {
				CPURequest    string `json:"cpuRequest,omitempty"`
				MemoryRequest string `json:"memoryRequest,omitempty"`
				CPULimit      string `json:"cpuLimit,omitempty"`
				MemoryLimit   string `json:"memoryLimit,omitempty"`
			} `json:"resources,omitempty"`
			HealthCheckPath string `json:"healthCheckPath,omitempty"`
			HealthCheckPort int32  `json:"healthCheckPort,omitempty"`
		} `json:"spec"`
	}{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fmt.Sprintf("%s/%s", GROUP, MajorVersion),
			Kind:       "WorkloadService",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Set spec fields
	serviceConfig.Spec.Image = image
	serviceConfig.Spec.Script.Commands = commands
	serviceConfig.Spec.Script.Shell = shell
	serviceConfig.Spec.Replicas = replicas
	serviceConfig.Spec.Env = env
	serviceConfig.Spec.Resources.CPURequest = resources.CPURequest
	serviceConfig.Spec.Resources.MemoryRequest = resources.MemoryRequest
	serviceConfig.Spec.Resources.CPULimit = resources.CPULimit
	serviceConfig.Spec.Resources.MemoryLimit = resources.MemoryLimit
	serviceConfig.Spec.HealthCheckPath = healthCheckPath
	serviceConfig.Spec.HealthCheckPort = healthCheckPort

	// Set ports
	serviceConfig.Spec.Ports = make([]struct {
		Name          string `json:"name"`
		ContainerPort int32  `json:"containerPort"`
		ServicePort   int32  `json:"servicePort"`
	}, len(ports))

	for i, port := range ports {
		serviceConfig.Spec.Ports[i].Name = port.Name
		serviceConfig.Spec.Ports[i].ContainerPort = port.ContainerPort
		serviceConfig.Spec.Ports[i].ServicePort = port.ServicePort
	}

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(serviceConfig)
	if err != nil {
		return fmt.Errorf("failed to convert ServiceConfig to unstructured: %w", err)
	}

	// Create the unstructured object
	obj := &unstructured.Unstructured{Object: unstructuredObj}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: ServicesResource,
	}

	// Create the resource
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create WorkloadService '%s/%s': %w", namespace, name, err)
	}

	klog.Infof("Successfully created WorkloadService '%s/%s'", namespace, name)
	return nil
}

// CreateCronJobCRD creates a WorkloadCronJob custom resource
func CreateCronJobCRD(ctx context.Context, dynamicClient dynamic.Interface, name, namespace, image string,
	commands []string, shell string, schedule string, env map[string]string,
	resources ResourceConfig, restartPolicy string) error {

	// Create a typed object first
	cronJobConfig := &struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			Image  string `json:"image"`
			Script struct {
				Commands []string `json:"commands"`
				Shell    string   `json:"shell"`
			} `json:"script"`
			Schedule  string            `json:"schedule"`
			Env       map[string]string `json:"env,omitempty"`
			Resources struct {
				CPURequest    string `json:"cpuRequest,omitempty"`
				MemoryRequest string `json:"memoryRequest,omitempty"`
				CPULimit      string `json:"cpuLimit,omitempty"`
				MemoryLimit   string `json:"memoryLimit,omitempty"`
			} `json:"resources,omitempty"`
			RestartPolicy string `json:"restartPolicy,omitempty"`
		} `json:"spec"`
	}{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fmt.Sprintf("%s/%s", GROUP, MajorVersion),
			Kind:       "WorkloadCronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Set spec fields
	cronJobConfig.Spec.Image = image
	cronJobConfig.Spec.Script.Commands = commands
	cronJobConfig.Spec.Script.Shell = shell
	cronJobConfig.Spec.Schedule = schedule
	cronJobConfig.Spec.Env = env
	cronJobConfig.Spec.Resources.CPURequest = resources.CPURequest
	cronJobConfig.Spec.Resources.MemoryRequest = resources.MemoryRequest
	cronJobConfig.Spec.Resources.CPULimit = resources.CPULimit
	cronJobConfig.Spec.Resources.MemoryLimit = resources.MemoryLimit
	cronJobConfig.Spec.RestartPolicy = restartPolicy

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cronJobConfig)
	if err != nil {
		return fmt.Errorf("failed to convert CronJobConfig to unstructured: %w", err)
	}

	// Create the unstructured object
	obj := &unstructured.Unstructured{Object: unstructuredObj}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: CronjobsResource,
	}

	// Create the resource
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create WorkloadCronJob '%s/%s': %w", namespace, name, err)
	}

	klog.Infof("Successfully created WorkloadCronJob '%s/%s'", namespace, name)
	return nil
}

// CreatePersistentCRD creates a WorkloadPersistent custom resource
func CreatePersistentCRD(ctx context.Context, dynamicClient dynamic.Interface, name, namespace, image string,
	commands []string, shell string, replicas int32, ports []PortConfig, env map[string]string,
	resources ResourceConfig, serviceName string, healthCheckPath string, healthCheckPort int32,
	persistentVolumes []VolumeConfig) error {

	// Create a typed object first
	persistentConfig := &struct {
		metav1.TypeMeta   `json:",inline"`
		metav1.ObjectMeta `json:"metadata,omitempty"`
		Spec              struct {
			Image  string `json:"image"`
			Script struct {
				Commands []string `json:"commands"`
				Shell    string   `json:"shell"`
			} `json:"script"`
			Replicas int32 `json:"replicas"`
			Ports    []struct {
				Name          string `json:"name"`
				ContainerPort int32  `json:"containerPort"`
				ServicePort   int32  `json:"servicePort"`
			} `json:"ports,omitempty"`
			Env       map[string]string `json:"env,omitempty"`
			Resources struct {
				CPURequest    string `json:"cpuRequest,omitempty"`
				MemoryRequest string `json:"memoryRequest,omitempty"`
				CPULimit      string `json:"cpuLimit,omitempty"`
				MemoryLimit   string `json:"memoryLimit,omitempty"`
			} `json:"resources,omitempty"`
			ServiceName       string `json:"serviceName,omitempty"`
			HealthCheckPath   string `json:"healthCheckPath,omitempty"`
			HealthCheckPort   int32  `json:"healthCheckPort,omitempty"`
			PersistentVolumes []struct {
				Name             string `json:"name"`
				MountPath        string `json:"mountPath"`
				Size             string `json:"size"`
				StorageClassName string `json:"storageClassName,omitempty"`
			} `json:"persistentVolumes,omitempty"`
		} `json:"spec"`
	}{
		TypeMeta: metav1.TypeMeta{
			APIVersion: fmt.Sprintf("%s/%s", GROUP, MajorVersion),
			Kind:       "WorkloadPersistent",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	// Set spec fields
	persistentConfig.Spec.Image = image
	persistentConfig.Spec.Script.Commands = commands
	persistentConfig.Spec.Script.Shell = shell
	persistentConfig.Spec.Replicas = replicas
	persistentConfig.Spec.Env = env
	persistentConfig.Spec.Resources.CPURequest = resources.CPURequest
	persistentConfig.Spec.Resources.MemoryRequest = resources.MemoryRequest
	persistentConfig.Spec.Resources.CPULimit = resources.CPULimit
	persistentConfig.Spec.Resources.MemoryLimit = resources.MemoryLimit
	persistentConfig.Spec.ServiceName = serviceName
	persistentConfig.Spec.HealthCheckPath = healthCheckPath
	persistentConfig.Spec.HealthCheckPort = healthCheckPort

	// Set ports
	persistentConfig.Spec.Ports = make([]struct {
		Name          string `json:"name"`
		ContainerPort int32  `json:"containerPort"`
		ServicePort   int32  `json:"servicePort"`
	}, len(ports))

	for i, port := range ports {
		persistentConfig.Spec.Ports[i].Name = port.Name
		persistentConfig.Spec.Ports[i].ContainerPort = port.ContainerPort
		persistentConfig.Spec.Ports[i].ServicePort = port.ServicePort
	}

	// Set persistent volumes
	persistentConfig.Spec.PersistentVolumes = make([]struct {
		Name             string `json:"name"`
		MountPath        string `json:"mountPath"`
		Size             string `json:"size"`
		StorageClassName string `json:"storageClassName,omitempty"`
	}, len(persistentVolumes))

	for i, volume := range persistentVolumes {
		persistentConfig.Spec.PersistentVolumes[i].Name = volume.Name
		persistentConfig.Spec.PersistentVolumes[i].MountPath = volume.MountPath
		persistentConfig.Spec.PersistentVolumes[i].Size = volume.Size
		persistentConfig.Spec.PersistentVolumes[i].StorageClassName = volume.StorageClassName
	}

	// Convert to unstructured
	unstructuredObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(persistentConfig)
	if err != nil {
		return fmt.Errorf("failed to convert PersistentConfig to unstructured: %w", err)
	}

	// Create the unstructured object
	obj := &unstructured.Unstructured{Object: unstructuredObj}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    GROUP,
		Version:  MajorVersion,
		Resource: PersistentsResource,
	}

	// Create the resource
	_, err = dynamicClient.Resource(gvr).Namespace(namespace).Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create WorkloadPersistent '%s/%s': %w", namespace, name, err)
	}

	klog.Infof("Successfully created WorkloadPersistent '%s/%s'", namespace, name)
	return nil
}

// Helper function to convert ResourceConfig to map
func resourceConfigToMap(resources ResourceConfig) map[string]interface{} {
	return map[string]interface{}{
		"cpuRequest":    resources.CPURequest,
		"memoryRequest": resources.MemoryRequest,
		"cpuLimit":      resources.CPULimit,
		"memoryLimit":   resources.MemoryLimit,
	}
}
