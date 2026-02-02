package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"

	"open-cluster-management.io/ocm/mcp/pkg/server"
)

// getKubeConfig returns a Kubernetes REST config
func getKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Try to use kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		// Fall back to in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to get kubeconfig: %v", err)
		}
	}

	return config, nil
}

func main() {
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	klog.InitFlags(nil)
	flag.Parse()

	config, err := getKubeConfig(kubeconfig)
	if err != nil {
		klog.ErrorS(err, "Failed to get kubeconfig")
		os.Exit(1)
	}

	clusterClient, err := clusterclientset.NewForConfig(config)
	if err != nil {
		klog.ErrorS(err, "Failed to create cluster client")
		os.Exit(1)
	}

	mcpServer := server.NewMCPServer(clusterClient)

	ctx := context.Background()
	if err := mcpServer.Run(ctx); err != nil {
		klog.ErrorS(err, "Server error")
		os.Exit(1)
	}
}
