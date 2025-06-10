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

// Package common provides shared types and utilities for k8-highlander.
//
// This file implements the configuration structures and initialization logic
// used throughout the k8-highlander system. It defines the configuration formats
// for different workload types and the main application settings, with support
// for multiple configuration sources (files, environment variables, defaults).
//
// Key features:
// - Flexible configuration loading from files and environment variables
// - Type-safe configuration structures for each workload type
// - Storage backend configuration (Redis or database)
// - Kubernetes cluster connection management
// - Configuration validation
// - Environment variable override support
//
// The configuration system provides a layered approach with the following precedence:
// 1. Command-line flags (handled elsewhere)
// 2. Environment variables
// 3. Configuration files
// 4. Default values
//
// This design ensures flexibility across different deployment environments while
// maintaining type safety and validation.

package common

import (
	"cloud.google.com/go/spanner"
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/klog/v2"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ClusterConfig holds configuration for a Kubernetes cluster
type ClusterConfig struct {
	Name               string        `json:"name,omitempty" yaml:"name,omitempty"`
	Kubeconfig         string        `json:"kubeconfig,omitempty" yaml:"kubeconfig,omitempty"`
	SelectedKubeconfig *rest.Config  `json:"-" yaml:"-"` // Not serialized
	clientHolder       *ClientHolder `json:"-" yaml:"-"` // Not serialized
}

type ClientHolder struct {
	clientMutex   sync.RWMutex
	client        kubernetes.Interface
	dynamicClient dynamic.Interface
}

func (c *ClusterConfig) GetClient() kubernetes.Interface {
	if c.clientHolder == nil {
		return nil
	}
	c.clientHolder.clientMutex.RLock()
	defer c.clientHolder.clientMutex.RUnlock()
	return c.clientHolder.client
}

func (c *ClusterConfig) GetDynamicClient() dynamic.Interface {
	if c.clientHolder == nil {
		return nil
	}
	c.clientHolder.clientMutex.RLock()
	defer c.clientHolder.clientMutex.RUnlock()
	return c.clientHolder.dynamicClient
}

func (c *ClusterConfig) SetClient(client kubernetes.Interface, dynamicClient dynamic.Interface) {
	if c.clientHolder == nil {
		c.clientHolder = &ClientHolder{}
	}
	c.clientHolder.clientMutex.Lock()
	defer c.clientHolder.clientMutex.Unlock()
	c.clientHolder.client = client
	c.clientHolder.dynamicClient = dynamicClient
}

// CronJobConfig defines the configuration for a cron job workload
type CronJobConfig struct {
	BaseWorkloadConfig `json:",inline" yaml:",inline"`
	Schedule           string `json:"schedule" yaml:"schedule"`
}

// PersistentConfig defines the configuration for a stateful set workload
type PersistentConfig struct {
	BaseWorkloadConfig `json:",inline" yaml:",inline"`
	HealthCheckPath    string         `json:"healthCheckPath,omitempty" yaml:"healthCheckPath,omitempty"`
	HealthCheckPort    int32          `json:"healthCheckPort,omitempty" yaml:"healthCheckPort,omitempty"`
	ReadinessTimeout   time.Duration  `json:"readinessTimeout,omitempty" yaml:"readinessTimeout,omitempty"`
	ServiceName        string         `json:"serviceName,omitempty" yaml:"serviceName,omitempty"`
	PersistentVolumes  []VolumeConfig `json:"persistentVolumes,omitempty" yaml:"persistentVolumes,omitempty"`
}

// ProcessConfig defines the configuration for a process workload
type ProcessConfig struct {
	BaseWorkloadConfig `json:",inline" yaml:",inline"`
	MaxRestarts        int           `json:"maxRestarts,omitempty" yaml:"maxRestarts,omitempty"`
	GracePeriod        time.Duration `json:"gracePeriod,omitempty" yaml:"gracePeriod,omitempty"`
}

// ServiceConfig defines the configuration for a continuously running service workload
type ServiceConfig struct {
	BaseWorkloadConfig `json:",inline" yaml:",inline"`
	HealthCheckPath    string        `json:"healthCheckPath,omitempty" yaml:"healthCheckPath,omitempty"`
	HealthCheckPort    int32         `json:"healthCheckPort,omitempty" yaml:"healthCheckPort,omitempty"`
	ReadinessTimeout   time.Duration `json:"readinessTimeout,omitempty" yaml:"readinessTimeout,omitempty"`
}

// StorageType represents the type of storage to use
type StorageType string

const (
	// StorageTypeRedis represents Redis storage
	StorageTypeRedis StorageType = "redis"

	// StorageTypeDB represents database storage
	StorageTypeDB StorageType = "db"

	// StorageTypeSpanner represents Google Cloud Spanner storage
	StorageTypeSpanner StorageType = "spanner"

	// StorageTypeMemory represents database storage
	StorageTypeMemory StorageType = "memory"
)

// StorageConfig contains configuration for storage
type StorageConfig struct {
	// Type is the type of storage to use
	Type StorageType

	// Redis configuration
	RedisClient *redis.Client

	// Database configuration
	DBClient *gorm.DB

	// Spanner configuration
	SpannerClient *spanner.Client
}

// AppConfig defines the application configuration
type AppConfig struct {
	// General configuration
	ID             string `yaml:"id"`
	Namespace      string `yaml:"namespace"`
	Tenant         string `yaml:"tenant"`
	Port           int    `yaml:"port" env:"PORT"`
	RemainFollower bool   `yaml:"remainsFollower" env:"REMAINS_FOLLOWER"`

	StorageType StorageType `yaml:"storageType" env:"STORAGE_TYPE"`

	// Redis configuration
	Redis struct {
		Addr     string `yaml:"addr" env:"REDIS_ADDR"`
		Password string `yaml:"password" env:"REDIS_PASSWORD"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`

	// Spanner configuration
	Spanner struct {
		ProjectID  string `yaml:"projectID" env:"SPANNER_PROJECT_ID"`
		InstanceID string `yaml:"instanceID" env:"SPANNER_INSTANCE_ID"`
		DatabaseID string `yaml:"databaseID" env:"SPANNER_DATABASE_ID"`
	} `yaml:"spanner"`

	DatabaseURL string `yaml:"databaseURL" env:"DATABASE_URL"`

	// Clusters configuration
	Cluster ClusterConfig `yaml:"cluster"`

	// Workloads configuration
	Workloads struct {
		Processes      []ProcessConfig    `yaml:"processes"`
		CronJobs       []CronJobConfig    `yaml:"cronJobs"`
		Services       []ServiceConfig    `yaml:"services"`
		PersistentSets []PersistentConfig `yaml:"persistentSets"`
	} `yaml:"workloads"`

	TerminationGracePeriod time.Duration `json:"terminationGracePeriod,omitempty" yaml:"terminationGracePeriod,omitempty"`
}

func (c *AppConfig) BuildStorageConfig() (_ *StorageConfig, err error) {
	if c.StorageType == "" {
		c.StorageType = StorageTypeRedis // Default to Redis
	}

	// Create storage based on type
	switch c.StorageType {
	case StorageTypeRedis:
		// Create Redis client
		redisClient := redis.NewClient(&redis.Options{
			Addr:     c.Redis.Addr,
			Password: c.Redis.Password,
			DB:       c.Redis.DB,
		})
		if err = verifyRedisConnection(c.Redis.Addr, redisClient); err != nil {
			return nil, err
		}
		return &StorageConfig{
			Type:        StorageTypeRedis,
			RedisClient: redisClient,
		}, nil
	case StorageTypeDB:
		// Create database client
		dbClient, err := gorm.Open(postgres.Open(c.DatabaseURL), &gorm.Config{})
		if err != nil {
			log.Fatalf("Failed to connect to database: %v", err)
		}

		return &StorageConfig{
			Type:     StorageTypeDB,
			DBClient: dbClient,
		}, nil
	case StorageTypeSpanner:
		if c.Spanner.ProjectID == "" || c.Spanner.InstanceID == "" || c.Spanner.DatabaseID == "" {
			return nil, NewSevereErrorMessage("AppConfig",
				"Spanner projectID, instanceID, and databaseID are required for Spanner storage")
		}
		spannerDB := fmt.Sprintf("projects/%s/instances/%s/databases/%s",
			c.Spanner.ProjectID, c.Spanner.InstanceID, c.Spanner.DatabaseID)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		spannerClient, err := spanner.NewClient(ctx, spannerDB)
		if err != nil {
			return nil, NewSevereError("AppConfig", fmt.Sprintf("failed to create Spanner client for %s", spannerDB), err)
		}
		klog.Infof("Connected to Spanner database: %s", spannerDB)
		return &StorageConfig{
			Type:          StorageTypeSpanner,
			SpannerClient: spannerClient,
		}, nil
	default:
		return nil, NewSevereErrorMessage("AppConfig", fmt.Sprintf("unsupported storage type: %s", c.StorageType))
	}
}

func (c *ClusterConfig) InitKubernetesClient() (err error) {
	if c.SelectedKubeconfig == nil {
		c.SelectedKubeconfig, err = getKubeConfigForCluster(c.Kubeconfig, c.Name)
		if err != nil {
			return err
		}
	}
	// Increase QPS and Burst limits to avoid rate limiting
	c.SelectedKubeconfig.QPS = 200
	c.SelectedKubeconfig.Burst = 200

	// Create clientset
	clientset, err := kubernetes.NewForConfig(c.SelectedKubeconfig)
	if err != nil {
		return fmt.Errorf("error creating kubernetes client: %w", err)
	}
	// Create a dynamic client for accessing CRDs
	dynamicClient, err := dynamic.NewForConfig(c.SelectedKubeconfig)
	if err != nil {
		klog.Warningf("Failed to create dynamic client for CRD loading: %v", err)
		return fmt.Errorf("error creating dynamic client: %w", err)
	}

	c.SetClient(clientset, dynamicClient)
	return nil
}

func verifyRedisConnection(addr string, rdb *redis.Client) error {
	// Test Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := rdb.Ping(ctx).Result()
	if err != nil {
		return NewSevereError("AppConfig", "failed to connect to Redis", err)
	}

	klog.Infof("Connected to Redis at %s", addr)

	// List all keys related to leader election
	keys, err := rdb.Keys(ctx, "highlander-leader-*").Result()
	if err != nil {
		klog.Warningf("Failed to list leader election keys: %v", err)
	} else {
		klog.Infof("Found %d leader election keys in Redis", len(keys))
		for _, key := range keys {
			val, err := rdb.Get(ctx, key).Result()
			if err != nil {
				klog.Warningf("Failed to get value for key %s: %v", key, err)
			} else {
				klog.Infof("Key: %s, Value: %s", key, val)
			}
		}
	}
	return nil
}

// InitConfig initializes the application configuration from various sources
// and loads any CRD-based workload configurations.
func InitConfig(cfgFile string) (appConfig AppConfig, err error) {
	// Load the basic configuration
	appConfig, err = initConfigFromFile(cfgFile)
	if err != nil {
		return appConfig, err
	}

	// Create a Kubernetes client config that respects the cluster name
	appConfig.Cluster.SelectedKubeconfig, err = getKubeConfigForCluster(
		appConfig.Cluster.Kubeconfig, appConfig.Cluster.Name)
	if err != nil {
		klog.Warningf("Failed to create Kubernetes config for CRD loading: %v", err)
		return appConfig, err
	}

	err = appConfig.Cluster.InitKubernetesClient()
	if err != nil {
		return appConfig, err
	}

	// Check if any workloads have CRD references
	hasCRDRefs := checkForCRDReferences(&appConfig)

	// If we have CRD references, we need to load them
	if hasCRDRefs {
		// Load CRD configurations
		if err := loadCRDConfigurations(context.Background(), &appConfig, appConfig.Cluster.GetDynamicClient()); err != nil {
			klog.Warningf("Failed to load CRD configurations: %v", err)
			return appConfig, err
		}
	}

	return appConfig, nil
}

func initConfigFromFile(cfgFile string) (appConfig AppConfig, err error) {
	var rawConfig string

	// Setup Viper for environment variables
	viper.SetEnvPrefix("HIGHLANDER")
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Bind specific environment variables
	_ = viper.BindEnv("id", "HIGHLANDER_ID")
	_ = viper.BindEnv("tenant", "HIGHLANDER_TENANT")
	_ = viper.BindEnv("port", "HIGHLANDER_PORT")
	_ = viper.BindEnv("namespace", "HIGHLANDER_NAMESPACE")
	_ = viper.BindEnv("storageType", "HIGHLANDER_STORAGE_TYPE")
	_ = viper.BindEnv("redis.addr", "HIGHLANDER_REDIS_ADDR")
	_ = viper.BindEnv("redis.password", "HIGHLANDER_REDIS_PASSWORD")
	_ = viper.BindEnv("redis.db", "HIGHLANDER_REDIS_DB")
	_ = viper.BindEnv("databaseURL", "HIGHLANDER_DATABASE_URL")
	_ = viper.BindEnv("cluster.kubeconfig", "HIGHLANDER_KUBECONFIG")
	_ = viper.BindEnv("cluster.name", "HIGHLANDER_CLUSTER_NAME")
	_ = viper.BindEnv("spanner.projectID", "HIGHLANDER_SPANNER_PROJECT_ID")
	_ = viper.BindEnv("spanner.instanceID", "HIGHLANDER_SPANNER_INSTANCE_ID")
	_ = viper.BindEnv("spanner.databaseID", "HIGHLANDER_SPANNER_DATABASE_ID")

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
		// Read the raw file content
		fileContent, err := os.ReadFile(cfgFile)
		if err == nil {
			rawConfig = string(fileContent)
			klog.V(4).Infof("Read raw config file: %s", cfgFile)
		} else {
			klog.Warningf("Failed to read raw config file for debugging: %v", err)
		}
	} else {
		// Search for config in default locations
		viper.AddConfigPath("/etc/k8-highlander/")
		viper.AddConfigPath("$HOME/.k8-highlander")
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	// If a config file is found, read it in.
	if err := viper.ReadInConfig(); err == nil {
		klog.Infof("Using config file: %s", viper.ConfigFileUsed())
		if rawConfig == "" && viper.ConfigFileUsed() != "" {
			fileContent, err := os.ReadFile(viper.ConfigFileUsed())
			if err == nil {
				rawConfig = string(fileContent)
			} else {
				klog.Warningf("Failed to read raw config file for debugging: %v", err)
			}
		}
	} else {
		klog.Infof("Error reading config file: %s, using flags and environment variables: %s", cfgFile, err)
	}

	// Parse the YAML directly instead of using Viper's Unmarshal
	// This is more reliable for complex nested structures
	if rawConfig != "" {
		err := yaml.Unmarshal([]byte(rawConfig), &appConfig)
		if err != nil {
			return appConfig, fmt.Errorf("unable to parse config YAML: %w", err)
		}
	} else {
		// Fall back to Viper if we couldn't read the raw config
		if err := viper.Unmarshal(&appConfig); err != nil {
			return appConfig, fmt.Errorf("unable to decode config into struct: %w", err)
		}
	}

	// Apply environment variable overrides after parsing YAML
	applyEnvironmentOverrides(&appConfig)

	// Set ID from hostname if not specified
	if appConfig.ID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return appConfig, fmt.Errorf("failed to get hostname: %w", err)
		}
		appConfig.ID = hostname
	}

	// Set default kubeconfig if not specified
	if appConfig.Cluster.Kubeconfig == "" {
		homeDir, err := os.UserHomeDir()
		if err == nil {
			defaultKubeconfig := filepath.Join(homeDir, ".kube", "config")
			if _, err := os.Stat(defaultKubeconfig); err == nil {
				appConfig.Cluster.Kubeconfig = defaultKubeconfig
			}
		}
	}

	klog.Infof("Configuration:")
	klog.Infof("ID: %s", appConfig.ID)
	klog.Infof("Tenant: %s", appConfig.Tenant)
	klog.Infof("Namespace: %s", appConfig.Namespace)
	klog.Infof("Cluster Name: %s", appConfig.Cluster.Name)
	klog.Infof("Cluster Kubeconfig: %s", appConfig.Cluster.Kubeconfig)
	if appConfig.StorageType == StorageTypeSpanner {
		klog.Infof("Spanner Project: %s, Instance: %s, Database: %s",
			appConfig.Spanner.ProjectID, appConfig.Spanner.InstanceID, appConfig.Spanner.DatabaseID)
	} else if appConfig.StorageType == StorageTypeDB {
		klog.Infof("DB: %s", appConfig.DatabaseURL)
	} else if appConfig.StorageType == StorageTypeRedis {
		klog.Infof("Redis: %s", appConfig.Redis.Addr)
	}

	// Validate the configuration
	if err = appConfig.Validate(); err != nil {
		if rawConfig != "" {
			klog.Errorf("Invalid configuration file that failed validation:\n%s", rawConfig)
		}
		return appConfig, fmt.Errorf("invalid configuration: %w", err)
	}
	return
}

// applyEnvironmentOverrides applies environment variable overrides to the config
func applyEnvironmentOverrides(config *AppConfig) {
	// Check for environment variables and override config values
	if envID := os.Getenv("HIGHLANDER_ID"); envID != "" {
		config.ID = envID
	}

	if envTenant := os.Getenv("HIGHLANDER_TENANT"); envTenant != "" {
		config.Tenant = envTenant
	}

	if envPort := os.Getenv("HIGHLANDER_PORT"); envPort != "" {
		if port, err := strconv.Atoi(envPort); err == nil {
			config.Port = port
		}
	}

	if envNamespace := os.Getenv("HIGHLANDER_NAMESPACE"); envNamespace != "" {
		config.Namespace = envNamespace
	}

	if envStorageType := os.Getenv("HIGHLANDER_STORAGE_TYPE"); envStorageType != "" {
		config.StorageType = StorageType(envStorageType)
	}

	if envRedisAddr := os.Getenv("HIGHLANDER_REDIS_ADDR"); envRedisAddr != "" {
		config.Redis.Addr = envRedisAddr
	}

	if envRedisPassword := os.Getenv("HIGHLANDER_REDIS_PASSWORD"); envRedisPassword != "" {
		config.Redis.Password = envRedisPassword
	}

	if envRedisDB := os.Getenv("HIGHLANDER_REDIS_DB"); envRedisDB != "" {
		if db, err := strconv.Atoi(envRedisDB); err == nil {
			config.Redis.DB = db
		}
	}

	if envSpannerProjectID := os.Getenv("HIGHLANDER_SPANNER_PROJECT_ID"); envSpannerProjectID != "" {
		config.Spanner.ProjectID = envSpannerProjectID
	}
	if envSpannerInstanceID := os.Getenv("HIGHLANDER_SPANNER_INSTANCE_ID"); envSpannerInstanceID != "" {
		config.Spanner.InstanceID = envSpannerInstanceID
	}
	if envSpannerDatabaseID := os.Getenv("HIGHLANDER_SPANNER_DATABASE_ID"); envSpannerDatabaseID != "" {
		config.Spanner.DatabaseID = envSpannerDatabaseID
	}

	if envDatabaseURL := os.Getenv("HIGHLANDER_DATABASE_URL"); envDatabaseURL != "" {
		config.DatabaseURL = envDatabaseURL
	}

	if envKubeconfig := os.Getenv("HIGHLANDER_KUBECONFIG"); envKubeconfig != "" {
		config.Cluster.Kubeconfig = envKubeconfig
	}

	if envClusterName := os.Getenv("HIGHLANDER_CLUSTER_NAME"); envClusterName != "" {
		config.Cluster.Name = envClusterName
	}
}

// checkForCRDReferences checks if any workloads have CRD references
func checkForCRDReferences(config *AppConfig) bool {
	// Check processes
	for _, proc := range config.Workloads.Processes {
		if proc.WorkloadCRDRef != nil {
			return true
		}
	}

	// Check services
	for _, svc := range config.Workloads.Services {
		if svc.WorkloadCRDRef != nil {
			return true
		}
	}

	// Check cron jobs
	for _, cj := range config.Workloads.CronJobs {
		if cj.WorkloadCRDRef != nil {
			return true
		}
	}

	// Check persistent sets
	for _, ps := range config.Workloads.PersistentSets {
		if ps.WorkloadCRDRef != nil {
			return true
		}
	}

	return false
}

// getKubeConfigForCluster creates a Kubernetes client config that specifically targets
// the named cluster from the kubeconfig file. This ensures we're connecting to the right cluster
// when the kubeconfig contains multiple clusters.
func getKubeConfigForCluster(kubeconfigPath, clusterName string) (*rest.Config, error) {
	// If no kubeconfig is specified, try in-cluster config
	if kubeconfigPath == "" {
		return rest.InClusterConfig()
	}

	// Load the kubeconfig file
	config, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("error loading kubeconfig from %s: %w", kubeconfigPath, err)
	}

	// If no cluster name is specified, use the current context's cluster
	if clusterName == "" {
		// Use the default context
		if config.CurrentContext == "" {
			return nil, fmt.Errorf("no current context found in kubeconfig and no cluster name specified")
		}

		// Get the context
		context, exists := config.Contexts[config.CurrentContext]
		if !exists {
			return nil, fmt.Errorf("current context %s not found in kubeconfig", config.CurrentContext)
		}

		// Use the cluster from the context
		clusterName = context.Cluster
	}

	// Check if the specified cluster exists
	_, exists := config.Clusters[clusterName]
	if !exists {
		klog.Warningf("invalid kubeconfig: %s, config: %v", kubeconfigPath, config)
		return nil, fmt.Errorf("cluster %s not found in kubeconfig", clusterName)
	}

	// Create a REST config specifically for the named cluster
	configOverrides := &clientcmd.ConfigOverrides{
		ClusterInfo: api.Cluster{
			Server: config.Clusters[clusterName].Server,
		},
		CurrentContext: "", // Don't use the current context
	}

	// Use the specified cluster
	configOverrides.Context.Cluster = clusterName

	// Create a ClientConfig with the specified overrides
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfigPath},
		configOverrides,
	)

	// Create the REST config
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("error creating REST config for cluster %s: %w", clusterName, err)
	}

	// Increase QPS and Burst limits to avoid rate limiting
	restConfig.QPS = 100
	restConfig.Burst = 100

	return restConfig, nil
}
