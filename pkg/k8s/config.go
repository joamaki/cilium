// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

// Package k8s abstracts all Kubernetes specific behaviour
package k8s

import (
	"os"
	"strings"
)

var (
	// config is the configuration of Kubernetes related settings
	config configuration
)

type configuration struct {
	// APIServerURL is the URL address of the API server
	APIServerURL string

	// KubeconfigPath is the local path to the kubeconfig configuration
	// file on the filesystem
	KubeconfigPath string

	// QPS is the QPS to pass to the kubernetes client configuration.
	QPS float32

	// Burst is the burst to pass to the kubernetes client configuration.
	Burst int
}

// GetAPIServerURL returns the configured API server URL address
func GetAPIServerURL() string {
	return config.APIServerURL
}

// GetKubeconfigPath returns the configured path to the kubeconfig
// configuration file
func GetKubeconfigPath() string {
	return config.KubeconfigPath
}

// GetQPS gets the QPS of the K8s configuration.
func GetQPS() float32 {
	return config.QPS
}

// GetBurst gets the burst limit of the K8s configuration.
func GetBurst() int {
	return config.Burst
}

// Configure sets the parameters of the Kubernetes package
func Configure(apiServerURL, kubeconfigPath string, qps float32, burst int) {
	config.APIServerURL = apiServerURL
	config.KubeconfigPath = kubeconfigPath
	config.QPS = qps
	config.Burst = burst

	if IsEnabled() &&
		config.APIServerURL != "" &&
		!strings.HasPrefix(apiServerURL, "http") {
		config.APIServerURL = "http://" + apiServerURL // default to HTTP
	}
}

// IsEnabled checks if Cilium is being used in tandem with Kubernetes.
func IsEnabled() bool {
	// If clients have been set, then consider k8s enabled regardless
	// of config.
	if k8sCLI.Interface != nil {
		return true
	}

	return config.APIServerURL != "" ||
		config.KubeconfigPath != "" ||
		(os.Getenv("KUBERNETES_SERVICE_HOST") != "" &&
			os.Getenv("KUBERNETES_SERVICE_PORT") != "") ||
		os.Getenv("K8S_NODE_NAME") != ""
}
