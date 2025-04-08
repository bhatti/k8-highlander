package common

import (
	"fmt"
	"github.com/spf13/viper"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfigValidation(t *testing.T) {
	// Create a temporary file with the YAML content
	yamlContent := `
id: ""  # Will use hostname by default
tenant: "test-tenant"
port: 8080
namespace: "default"

# Storage configuration
storageType: "redis"
redis:
  addr: "localhost:6379"
  password: ""
  db: 0

# Cluster configuration
cluster:
  name: "default"
  kubeconfig: "primary-kubeconfig.yaml"

# Workloads configuration
workloads:
  processes:
    - name: "singleton-echo"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton process'"
          - "echo 'Process ID: $$'"
          - "while true; do echo \"Singleton heartbeat at $(date)\"; sleep 10; done"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "Never"

  cronJobs:
    - name: "singleton-cron"
      schedule: "*/1 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running cron job at $(date)'"
          - "echo 'Hostname: $(hostname)'"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "50m"
        memoryRequest: "32Mi"
        cpuLimit: "100m"
        memoryLimit: "64Mi"
      restartPolicy: "OnFailure"

  services:
    - name: "singleton-service"
      replicas: 1
      image: "nginx:alpine"
      script:
        commands:
          - "nginx -g 'daemon off;'"
        shell: "/bin/sh"
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8181
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"

  persistentSets:
    - name: "singleton-persistent"
      replicas: 1
      image: "redis:alpine"
      script:
        commands:
          - "redis-server"
        shell: "/bin/sh"
      ports:
        - name: "redis"
          containerPort: 6379
          servicePort: 6379
      persistentVolumes:
        - name: "data"
          mountPath: "/data"
          size: "1Gi"
      resources:
        cpuRequest: "100m"
        memoryRequest: "128Mi"
        cpuLimit: "200m"
        memoryLimit: "256Mi"
`
	tmpfile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpfile.Name())

	_, err = tmpfile.Write([]byte(yamlContent))
	require.NoError(t, err)
	require.NoError(t, tmpfile.Close())

	// Parse the YAML into AppConfig
	var config AppConfig
	data, err := os.ReadFile(tmpfile.Name())
	require.NoError(t, err)

	t.Logf("Parsing YAML file: %s", tmpfile.Name())
	err = yaml.Unmarshal(data, &config)
	require.NoError(t, err, "Failed to unmarshal YAML")

	// Print the parsed config for debugging
	t.Logf("Parsed config: %+v", config)
	t.Logf("Services: %+v", config.Workloads.Services)

	// Check for specific fields that might be missing
	for i, service := range config.Workloads.Services {
		t.Logf("Service %d: %+v", i, service)
		t.Logf("Service %d script: %+v", i, service.Script)
	}

	for i, persistent := range config.Workloads.PersistentSets {
		t.Logf("PersistentSet %d: %+v", i, persistent)
		t.Logf("PersistentSet %d script: %+v", i, persistent.Script)
	}

	// Validate the config
	err = config.Validate()

	// If there's an error, print it for debugging
	if err != nil {
		t.Logf("Validation error: %v", err)
	}

	assert.NoError(t, err)
}

func TestInitConfig(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "config-test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	clusterConfig := `
apiVersion: v1
clusters:
- cluster:
    server: https://kubernetes.docker.internal:6443
  name: default
`
	clusterPath := filepath.Join(tempDir, "cluster-config.yaml")
	err = os.WriteFile(clusterPath, []byte(clusterConfig), 0644)
	require.NoError(t, err)

	// Create a valid config file
	validConfig := fmt.Sprintf(`
id: ""  # Will use hostname by default
tenant: "test-tenant"
port: 8080
namespace: "default"

# Storage configuration
storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

# Cluster configuration
cluster:
  name: "default"
  kubeconfig: "%s"

# Workloads configuration
workloads:
  processes:
    - name: "singleton-echo"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Starting singleton process'"
          - "echo 'Process ID: $$'"
          - "while true; do echo \"Singleton heartbeat at $(date)\"; sleep 10; done"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"
      restartPolicy: "Never"

  cronJobs:
    - name: "singleton-cron"
      schedule: "*/1 * * * *"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Running cron job at $(date)'"
          - "echo 'Hostname: $(hostname)'"
        shell: "/bin/sh"
      env:
        TEST_ENV: "test-value"
      resources:
        cpuRequest: "50m"
        memoryRequest: "32Mi"
        cpuLimit: "100m"
        memoryLimit: "64Mi"
      restartPolicy: "OnFailure"

  services:
    - name: "singleton-service"
      replicas: 1
      image: "nginx:alpine"
      script:
        commands:
          - "nginx -g 'daemon off;'"
        shell: "/bin/sh"
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8181
      resources:
        cpuRequest: "100m"
        memoryRequest: "64Mi"
        cpuLimit: "200m"
        memoryLimit: "128Mi"

  persistentSets:
    - name: "singleton-persistent"
      replicas: 1
      image: "redis:alpine"
      script:
        commands:
          - "redis-server"
        shell: "/bin/sh"
      ports:
        - name: "redis"
          containerPort: 6379
          servicePort: 6379
      persistentVolumes:
        - name: "data"
          mountPath: "/data"
          size: "1Gi"
      resources:
        cpuRequest: "100m"
        memoryRequest: "128Mi"
        cpuLimit: "200m"
        memoryLimit: "256Mi"
`, clusterPath)

	validConfigPath := filepath.Join(tempDir, "valid-config.yaml")
	err = os.WriteFile(validConfigPath, []byte(validConfig), 0644)
	require.NoError(t, err)

	// Test loading and validating a valid config
	t.Run("TestValidConfig", func(t *testing.T) {
		// Reset viper for each test
		viper.Reset()

		// Load the config using the common function
		config, err := InitConfig(validConfigPath)
		require.NoError(t, err, "InitConfig should succeed with valid config")

		// Verify the config was loaded correctly
		assert.Equal(t, "test-tenant", config.Tenant, "Tenant should be loaded correctly")
		assert.Equal(t, 8080, config.Port, "Port should be loaded correctly")
		assert.Equal(t, "default", config.Namespace, "Namespace should be loaded correctly")
		assert.Equal(t, "default", config.Cluster.Name, "Cluster name should be loaded correctly")

		// Verify workloads were loaded correctly
		assert.Len(t, config.Workloads.Processes, 1, "Should have 1 process")
		assert.Len(t, config.Workloads.CronJobs, 1, "Should have 1 cron job")
		assert.Len(t, config.Workloads.Services, 1, "Should have 1 service")
		assert.Len(t, config.Workloads.PersistentSets, 1, "Should have 1 persistent set")

		// Verify process details
		process := config.Workloads.Processes[0]
		assert.Equal(t, "singleton-echo", process.Name, "Process name should be loaded correctly")
		assert.Equal(t, "busybox:latest", process.Image, "Process image should be loaded correctly")
		assert.NotNil(t, process.Script, "Process script should be loaded")
		assert.Len(t, process.Script.Commands, 3, "Process script should have 3 commands")

		// Verify service details
		service := config.Workloads.Services[0]
		assert.Equal(t, "singleton-service", service.Name, "Service name should be loaded correctly")
		assert.Equal(t, "nginx:alpine", service.Image, "Service image should be loaded correctly")
		assert.NotNil(t, service.Script, "Service script should be loaded")
		assert.Len(t, service.Script.Commands, 1, "Service script should have 1 command")
		assert.Equal(t, int32(1), service.Replicas, "Service replicas should be loaded correctly")

		// Verify persistent set details
		persistentSet := config.Workloads.PersistentSets[0]
		assert.Equal(t, "singleton-persistent", persistentSet.Name, "PersistentSet name should be loaded correctly")
		assert.Equal(t, "redis:alpine", persistentSet.Image, "PersistentSet image should be loaded correctly")
		assert.NotNil(t, persistentSet.Script, "PersistentSet script should be loaded")
		assert.Len(t, persistentSet.Script.Commands, 1, "PersistentSet script should have 1 command")
		assert.Equal(t, int32(1), persistentSet.Replicas, "PersistentSet replicas should be loaded correctly")
	})

	// Test with a config that's missing scripts
	t.Run("TestMissingScripts", func(t *testing.T) {
		invalidConfig := `
id: ""
tenant: "test-tenant"
port: 8080
namespace: "default"

storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

cluster:
  name: "default"
  kubeconfig: "/home/sbhatti/k8-highlander/primary-kubeconfig.yaml"

workloads:
  services:
    - name: "singleton-service"
      replicas: 1
      image: "nginx:alpine"
      # Missing script section
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8181
`
		invalidConfigPath := filepath.Join(tempDir, "invalid-config.yaml")
		err = os.WriteFile(invalidConfigPath, []byte(invalidConfig), 0644)
		require.NoError(t, err)

		// Reset viper for each test
		viper.Reset()

		// Load the config using the common function
		_, err := InitConfig(invalidConfigPath)
		assert.Error(t, err, "InitConfig should fail with invalid config")
		assert.Contains(t, err.Error(), "script is required", "Error should mention missing script")
	})

	// Test with a config that has empty script commands
	t.Run("TestEmptyScriptCommands", func(t *testing.T) {
		invalidConfig := `
id: ""
tenant: "test-tenant"
port: 8080
namespace: "default"

storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

cluster:
  name: "default"
  kubeconfig: "/home/sbhatti/k8-highlander/primary-kubeconfig.yaml"

workloads:
  services:
    - name: "singleton-service"
      replicas: 1
      image: "nginx:alpine"
      script:
        commands: []  # Empty commands array
        shell: "/bin/sh"
      ports:
        - name: "http"
          containerPort: 80
          servicePort: 8181
`
		invalidConfigPath := filepath.Join(tempDir, "empty-commands-config.yaml")
		err = os.WriteFile(invalidConfigPath, []byte(invalidConfig), 0644)
		require.NoError(t, err)

		// Reset viper for each test
		viper.Reset()

		// Load the config using the common function
		_, err := InitConfig(invalidConfigPath)
		assert.Error(t, err, "InitConfig should fail with empty commands")
		assert.Contains(t, err.Error(), "at least one command is required", "Error should mention empty commands")
	})

	// Test with a non-existent config file
	t.Run("TestNonExistentFile", func(t *testing.T) {
		// Reset viper for each test
		viper.Reset()

		// Try to load a non-existent config file
		_, err := InitConfig(filepath.Join(tempDir, "non-existent-config.yaml"))
		assert.Error(t, err, "InitConfig should fail with non-existent file")
	})

	// Test with environment variables
	t.Run("TestEnvironmentVariables", func(t *testing.T) {
		// Reset viper for each test
		viper.Reset()

		// Set environment variables
		_ = os.Setenv("HIGHLANDER_TENANT", "env-tenant")
		_ = os.Setenv("HIGHLANDER_PORT", "9090")
		_ = os.Setenv("HIGHLANDER_CLUSTER_NAME", "default")
		defer func() {
			_ = os.Unsetenv("HIGHLANDER_TENANT")
			_ = os.Unsetenv("HIGHLANDER_PORT")
			_ = os.Unsetenv("HIGHLANDER_CLUSTER_NAME")
		}()

		minimalConfigPath := filepath.Join(tempDir, "minimal-config.yaml")
		// Create a minimal config file that will be supplemented by env vars
		minimalConfig := fmt.Sprintf(`
id: ""
namespace: "default"

storageType: "redis"
redis:
  addr: "10.1.1.1:6379"
  password: ""
  db: 0

cluster:
  kubeconfig: "%s"

workloads:
  processes:
    - name: "singleton-echo"
      image: "busybox:latest"
      script:
        commands:
          - "echo 'Hello'"
        shell: "/bin/sh"
`, clusterPath)
		err = os.WriteFile(minimalConfigPath, []byte(minimalConfig), 0644)
		require.NoError(t, err)

		// Load the config using the common function
		config, err := InitConfig(minimalConfigPath)
		require.NoError(t, err, "InitConfig should succeed with minimal config + env vars")

		// Verify environment variables were applied
		assert.Equal(t, "env-tenant", config.Tenant, "Tenant should be loaded from env var")
		assert.Equal(t, 9090, config.Port, "Port should be loaded from env var")
		assert.Equal(t, "default", config.Cluster.Name, "Cluster name should be loaded from env var")
	})
}
