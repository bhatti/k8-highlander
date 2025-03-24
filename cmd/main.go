package main

import (
	"github.com/bhatti/k8-highlander/pkg/cmd"
	"github.com/bhatti/k8-highlander/pkg/common"
)

func init() {
	// Set version constants
	common.VERSION = "0.0.1"
	common.BuildInfo = "development"
}

func main() {
	cmd.Execute()
}
