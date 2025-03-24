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
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"k8s.io/klog/v2"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
)

// ClusterConfig holds configuration for a Kubernetes cluster
type ClusterConfig struct {
	Name         string
	Kubeconfig   string
	clientHolder *ClientHolder
}

type ClientHolder struct {
	clientMutex sync.RWMutex
	client      kubernetes.Interface
}

func (c *ClusterConfig) GetClient() kubernetes.Interface {
	if c.clientHolder == nil {
		return nil
	}
	c.clientHolder.clientMutex.RLock()
	defer c.clientHolder.clientMutex.RUnlock()
	return c.clientHolder.client
}

func (c *ClusterConfig) SetClient(client kubernetes.Interface) {
	if c.clientHolder == nil {
		c.clientHolder = &ClientHolder{}
	}
	c.clientHolder.clientMutex.Lock()
	defer c.clientHolder.clientMutex.Unlock()
	c.clientHolder.client = client
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
)

// StorageConfig contains configuration for storage
type StorageConfig struct {
	// Type is the type of storage to use
	Type StorageType

	// Redis configuration
	RedisClient *redis.Client

	// Database configuration
	DBClient *gorm.DB
}

// AppConfig defines the application configuration
type AppConfig struct {
	// General configuration
	ID        string `yaml:"id"`
	Namespace string `yaml:"namespace"`
	Tenant    string `yaml:"tenant"`
	Port      int    `yaml:"port" env:"PORT"`

	StorageType StorageType `yaml:"storageType" env:"STORAGE_TYPE"`

	// Redis configuration
	Redis struct {
		Addr     string `yaml:"addr" env:"REDIS_ADDR"`
		Password string `yaml:"password" env:"REDIS_PASSWORD"`
		DB       int    `yaml:"db"`
	} `yaml:"redis"`

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
	default:
		return nil, NewSevereErrorMessage("AppConfig", fmt.Sprintf("unsupported storage type: %s", c.StorageType))
	}
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

func InitConfig(cfgFile string) (appConfig AppConfig, err error) {
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

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
		// Read the raw file content
		fileContent, err := os.ReadFile(cfgFile)
		if err == nil {
			rawConfig = string(fileContent)
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
		klog.Infof("No config file found, using flags and environment variables")
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
