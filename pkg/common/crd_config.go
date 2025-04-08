package common

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"time"
)

// loadCRDConfigurations loads workload configurations from CRDs
func loadCRDConfigurations(ctx context.Context, config *AppConfig, dynamicClient dynamic.Interface) error {
	// Process all processes with CRD references
	for i := range config.Workloads.Processes {
		proc := &config.Workloads.Processes[i]
		if proc.WorkloadCRDRef != nil {
			klog.Infof("Loading Process configuration from CRD: %s/%s",
				proc.WorkloadCRDRef.Namespace, proc.WorkloadCRDRef.Name)

			// Load the CRD and apply its configuration
			crdProc, err := LoadWorkloadFromCRD(ctx, dynamicClient, proc.WorkloadCRDRef)
			if err != nil {
				klog.Warningf("Failed to load Process CRD %s/%s: %v",
					proc.WorkloadCRDRef.Namespace, proc.WorkloadCRDRef.Name, err)
				continue // Skip this CRD but continue with others
			}

			// Convert to ProcessConfig
			crdConfig, ok := crdProc.(*ProcessConfig)
			if !ok {
				klog.Warningf("Failed to convert CRD to ProcessConfig: %v", err)
				continue
			}

			// Preserve the original name if needed
			originalName := proc.Name
			originalNamespace := proc.Namespace

			// Copy CRD configuration to the workload
			*proc = *crdConfig

			// Restore name and namespace if they were empty in the CRD
			if proc.Name == "" {
				proc.Name = originalName
			}
			if proc.Namespace == "" {
				proc.Namespace = originalNamespace
			}

			// Keep reference to the CRD
			proc.WorkloadCRDRef = crdConfig.WorkloadCRDRef
		}
	}

	// Process all services with CRD references
	for i := range config.Workloads.Services {
		svc := &config.Workloads.Services[i]
		if svc.WorkloadCRDRef != nil {
			klog.Infof("Loading Service configuration from CRD: %s/%s",
				svc.WorkloadCRDRef.Namespace, svc.WorkloadCRDRef.Name)

			// Load the CRD and apply its configuration
			crdSvc, err := LoadWorkloadFromCRD(ctx, dynamicClient, svc.WorkloadCRDRef)
			if err != nil {
				klog.Warningf("Failed to load Service CRD %s/%s: %v",
					svc.WorkloadCRDRef.Namespace, svc.WorkloadCRDRef.Name, err)
				continue // Skip this CRD but continue with others
			}

			// Convert to ServiceConfig
			crdConfig, ok := crdSvc.(*ServiceConfig)
			if !ok {
				klog.Warningf("Failed to convert CRD to ServiceConfig: %v", err)
				continue
			}

			// Preserve the original name if needed
			originalName := svc.Name
			originalNamespace := svc.Namespace

			// Copy CRD configuration to the workload
			*svc = *crdConfig

			// Restore name and namespace if they were empty in the CRD
			if svc.Name == "" {
				svc.Name = originalName
			}
			if svc.Namespace == "" {
				svc.Namespace = originalNamespace
			}

			// Keep reference to the CRD
			svc.WorkloadCRDRef = crdConfig.WorkloadCRDRef
		}
	}

	// Process all cron jobs with CRD references
	for i := range config.Workloads.CronJobs {
		cj := &config.Workloads.CronJobs[i]
		if cj.WorkloadCRDRef != nil {
			klog.Infof("Loading CronJob configuration from CRD: %s/%s",
				cj.WorkloadCRDRef.Namespace, cj.WorkloadCRDRef.Name)

			// Load the CRD and apply its configuration
			crdCj, err := LoadWorkloadFromCRD(ctx, dynamicClient, cj.WorkloadCRDRef)
			if err != nil {
				klog.Warningf("Failed to load CronJob CRD %s/%s: %v",
					cj.WorkloadCRDRef.Namespace, cj.WorkloadCRDRef.Name, err)
				continue // Skip this CRD but continue with others
			}

			// Convert to CronJobConfig
			crdConfig, ok := crdCj.(*CronJobConfig)
			if !ok {
				klog.Warningf("Failed to convert CRD to CronJobConfig: %v", err)
				continue
			}

			// Preserve the original name if needed
			originalName := cj.Name
			originalNamespace := cj.Namespace

			// Copy CRD configuration to the workload
			*cj = *crdConfig

			// Restore name and namespace if they were empty in the CRD
			if cj.Name == "" {
				cj.Name = originalName
			}
			if cj.Namespace == "" {
				cj.Namespace = originalNamespace
			}

			// Keep reference to the CRD
			cj.WorkloadCRDRef = crdConfig.WorkloadCRDRef
		}
	}

	// Process all persistent sets with CRD references
	for i := range config.Workloads.PersistentSets {
		ps := &config.Workloads.PersistentSets[i]
		if ps.WorkloadCRDRef != nil {
			klog.Infof("Loading PersistentSet configuration from CRD: %s/%s",
				ps.WorkloadCRDRef.Namespace, ps.WorkloadCRDRef.Name)

			// Load the CRD and apply its configuration
			crdPs, err := LoadWorkloadFromCRD(ctx, dynamicClient, ps.WorkloadCRDRef)
			if err != nil {
				klog.Warningf("Failed to load PersistentSet CRD %s/%s: %v",
					ps.WorkloadCRDRef.Namespace, ps.WorkloadCRDRef.Name, err)
				continue // Skip this CRD but continue with others
			}

			// Convert to PersistentConfig
			crdConfig, ok := crdPs.(*PersistentConfig)
			if !ok {
				klog.Warningf("Failed to convert CRD to PersistentConfig: %v", err)
				continue
			}

			// Preserve the original name if needed
			originalName := ps.Name
			originalNamespace := ps.Namespace

			// Copy CRD configuration to the workload
			*ps = *crdConfig

			// Restore name and namespace if they were empty in the CRD
			if ps.Name == "" {
				ps.Name = originalName
			}
			if ps.Namespace == "" {
				ps.Namespace = originalNamespace
			}

			// Keep reference to the CRD
			ps.WorkloadCRDRef = crdConfig.WorkloadCRDRef
		}
	}

	return nil
}

// LoadWorkloadFromCRD loads a workload configuration from a CRD reference
func LoadWorkloadFromCRD(ctx context.Context, dynamicClient dynamic.Interface, ref *WorkloadCRDReference) (interface{}, error) {
	if ref == nil {
		return nil, fmt.Errorf("workload CRD reference is nil")
	}

	// Determine the GVR based on the Kind
	var resource string
	switch ref.Kind {
	case "WorkloadProcess":
		resource = "workloadprocesses"
	case "WorkloadService":
		resource = "workloadservices"
	case "WorkloadCronJob":
		resource = "workloadcronjobs"
	case "WorkloadPersistent":
		resource = "workloadpersistents"
	default:
		return nil, fmt.Errorf("unsupported workload CRD kind: %s", ref.Kind)
	}

	// Create the GroupVersionResource
	gvr := schema.GroupVersionResource{
		Group:    "highlander.plexobject.io",
		Version:  "v1",
		Resource: resource,
	}

	// Use namespace from reference or default
	namespace := ref.Namespace
	if namespace == "" {
		namespace = "default"
	}

	// Fetch the CRD
	unstructuredObj, err := dynamicClient.Resource(gvr).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get %s '%s/%s': %w", ref.Kind, namespace, ref.Name, err)
	}

	// Convert based on the kind
	switch ref.Kind {
	case "WorkloadProcess":
		return convertToProcessConfig(unstructuredObj, ref.Name, namespace)
	case "WorkloadService":
		return convertToServiceConfig(unstructuredObj, ref.Name, namespace)
	case "WorkloadCronJob":
		return convertToCronJobConfig(unstructuredObj, ref.Name, namespace)
	case "WorkloadPersistent":
		return convertToPersistentConfig(unstructuredObj, ref.Name, namespace)
	default:
		return nil, fmt.Errorf("no converter for workload kind: %s", ref.Kind)
	}
}

// convertToProcessConfig converts an unstructured WorkloadProcess to ProcessConfig
func convertToProcessConfig(obj *unstructured.Unstructured, name, namespace string) (*ProcessConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadProcess: %w", err)
	}

	// Create a basic ProcessConfig
	config := &ProcessConfig{
		BaseWorkloadConfig: BaseWorkloadConfig{
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
		script := &ScriptConfig{}

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

	// Keep a reference to the CRD
	config.WorkloadCRDRef = &WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	return config, nil
}

// convertToServiceConfig converts an unstructured WorkloadService to ServiceConfig
func convertToServiceConfig(obj *unstructured.Unstructured, name, namespace string) (*ServiceConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadService: %w", err)
	}

	// Create a basic ServiceConfig
	config := &ServiceConfig{
		BaseWorkloadConfig: BaseWorkloadConfig{
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
		script := &ScriptConfig{}

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
				port := PortConfig{}

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

	// Keep a reference to the CRD
	config.WorkloadCRDRef = &WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	return config, nil
}

// convertToCronJobConfig converts an unstructured WorkloadCronJob to CronJobConfig
func convertToCronJobConfig(obj *unstructured.Unstructured, name, namespace string) (*CronJobConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadCronJob: %w", err)
	}

	// Create a basic CronJobConfig
	config := &CronJobConfig{
		BaseWorkloadConfig: BaseWorkloadConfig{
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
		script := &ScriptConfig{}

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

	// Keep a reference to the CRD
	config.WorkloadCRDRef = &WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	return config, nil
}

// convertToPersistentConfig converts an unstructured WorkloadPersistent to PersistentConfig
func convertToPersistentConfig(obj *unstructured.Unstructured, name, namespace string) (*PersistentConfig, error) {
	// Extract spec
	spec, found, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !found {
		return nil, fmt.Errorf("spec not found in WorkloadPersistent: %w", err)
	}

	// Create a basic PersistentConfig
	config := &PersistentConfig{
		BaseWorkloadConfig: BaseWorkloadConfig{
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
		script := &ScriptConfig{}

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
				port := PortConfig{}

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
				volume := VolumeConfig{}

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

	// Keep a reference to the CRD
	config.WorkloadCRDRef = &WorkloadCRDReference{
		APIVersion: obj.GetAPIVersion(),
		Kind:       obj.GetKind(),
		Name:       obj.GetName(),
		Namespace:  obj.GetNamespace(),
	}

	return config, nil
}
