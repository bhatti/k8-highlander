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

// Package cmd implements command-line interface for the k8-highlander controller.
//
// This package uses Cobra to implement a command-line interface with support for
// multiple configuration sources (command-line flags, environment variables, and
// configuration files). It provides a flexible way to configure the controller's
// behavior through the following priority order:
//   1. Command-line flags
//   2. Environment variables
//   3. Configuration file
//   4. Default values
//
// The command-line interface supports various configuration options including:
// - Controller identity and tenant
// - Kubernetes cluster configuration
// - Leader election storage (Redis or database)
// - HTTP/API server configuration

package cmd

import (
	"fmt"
	"github.com/bhatti/k8-highlander/pkg/common"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"k8s.io/klog/v2"
	"os"
)

var (
	cfgFile     string
	appConfig   common.AppConfig
	showVersion bool
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "k8-highlander",
	Short: "A controller for managing stateful workloads in Kubernetes",
	Long: `K8 Highlander manages singleton or stateful processes in Kubernetes clusters
where only one instance of the server should exist. It supports various workload types
including processes, cron jobs, services, and persistent workloads.`,
	Run: func(cmd *cobra.Command, args []string) {
		if showVersion {
			fmt.Printf("K8 Highlander %s (%s)\n", common.VERSION, common.BuildInfo)
			return
		}

		// Run the main controller
		if err := runController(); err != nil {
			klog.Fatalf("Failed to run controller: %v", err)
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is /etc/k8-highlander/config.yaml)")
	rootCmd.PersistentFlags().BoolVar(&showVersion, "version", false, "show version information")

	// Application configuration flags
	rootCmd.PersistentFlags().StringVar(&appConfig.ID, "id", "", "controller ID (default is hostname)")
	rootCmd.PersistentFlags().StringVar(&appConfig.Tenant, "tenant", "default", "tenant name")
	rootCmd.PersistentFlags().IntVar(&appConfig.Port, "port", 8080, "monitoring server port")
	rootCmd.PersistentFlags().String("storage-type", "redis", "storage type (redis or db)")
	rootCmd.PersistentFlags().String("redis-addr", "localhost:6379", "Redis server address")
	rootCmd.PersistentFlags().String("redis-password", "", "Redis server password")
	rootCmd.PersistentFlags().Int("redis-db", 0, "Redis database number")
	rootCmd.PersistentFlags().String("database-url", "", "Database connection URL")
	rootCmd.PersistentFlags().String("kubeconfig", "", "path to kubeconfig file")
	rootCmd.PersistentFlags().String("cluster-name", "default", "cluster name")

	// Bind flags to viper
	_ = viper.BindPFlag("id", rootCmd.PersistentFlags().Lookup("id"))
	_ = viper.BindPFlag("tenant", rootCmd.PersistentFlags().Lookup("tenant"))
	_ = viper.BindPFlag("port", rootCmd.PersistentFlags().Lookup("port"))
	_ = viper.BindPFlag("storageType", rootCmd.PersistentFlags().Lookup("storage-type"))
	_ = viper.BindPFlag("redis.addr", rootCmd.PersistentFlags().Lookup("redis-addr"))
	_ = viper.BindPFlag("redis.password", rootCmd.PersistentFlags().Lookup("redis-password"))
	_ = viper.BindPFlag("redis.db", rootCmd.PersistentFlags().Lookup("redis-db"))
	_ = viper.BindPFlag("databaseURL", rootCmd.PersistentFlags().Lookup("database-url"))
	_ = viper.BindPFlag("cluster.kubeconfig", rootCmd.PersistentFlags().Lookup("kubeconfig"))
	_ = viper.BindPFlag("cluster.name", rootCmd.PersistentFlags().Lookup("cluster-name"))

	// Bind environment variables
	viper.SetEnvPrefix("HIGHLANDER")
	viper.AutomaticEnv()
	_ = viper.BindEnv("id", "HIGHLANDER_ID")
	_ = viper.BindEnv("tenant", "HIGHLANDER_TENANT")
	_ = viper.BindEnv("port", "HIGHLANDER_PORT")
	_ = viper.BindEnv("storageType", "HIGHLANDER_STORAGE_TYPE")
	_ = viper.BindEnv("redis.addr", "HIGHLANDER_REDIS_ADDR")
	_ = viper.BindEnv("redis.password", "HIGHLANDER_REDIS_PASSWORD")
	_ = viper.BindEnv("redis.db", "HIGHLANDER_REDIS_DB")
	_ = viper.BindEnv("databaseURL", "HIGHLANDER_DATABASE_URL")
	_ = viper.BindEnv("cluster.kubeconfig", "HIGHLANDER_KUBECONFIG")
	_ = viper.BindEnv("cluster.name", "HIGHLANDER_CLUSTER_NAME")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	var err error
	appConfig, err = common.InitConfig(cfgFile)
	if err != nil {
		klog.Fatalf("config initialization failed: %v", err)
	}
}
