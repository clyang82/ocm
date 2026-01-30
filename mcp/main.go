package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

const (
	serverName    = "ocm-cluster-mcp"
	serverVersion = "0.1.0"
)

// MCPServer represents the MCP server for OCM cluster operations
type MCPServer struct {
	clusterClient clusterclientset.Interface
}

// MCPRequest represents an incoming MCP request
type MCPRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents an MCP response
type MCPResponse struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *MCPError       `json:"error,omitempty"`
}

// MCPError represents an MCP error
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerInfo represents server information
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ListClustersParams represents parameters for listing clusters
type ListClustersParams struct {
	LabelSelector string            `json:"labelSelector,omitempty"`
	Labels        map[string]string `json:"labels,omitempty"`
}

// ClusterInfo represents simplified cluster information
type ClusterInfo struct {
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels"`
	ClusterClaims map[string]string `json:"clusterClaims"`
	Status        string            `json:"status"`
	Accepted      bool              `json:"accepted"`
	Capacity      map[string]string `json:"capacity,omitempty"`
	Allocatable   map[string]string `json:"allocatable,omitempty"`
	KubeVersion   string            `json:"kubeVersion,omitempty"`
}

// ToolInfo represents an MCP tool
type ToolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// NewMCPServer creates a new MCP server
func NewMCPServer(kubeconfig string) (*MCPServer, error) {
	config, err := getKubeConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %v", err)
	}

	clusterClient, err := clusterclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster client: %v", err)
	}

	return &MCPServer{
		clusterClient: clusterClient,
	}, nil
}

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

// HandleRequest processes an MCP request
func (s *MCPServer) HandleRequest(ctx context.Context, req MCPRequest) MCPResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(ctx)
	case "tools/list":
		return s.handleToolsList(ctx)
	case "tools/call":
		return s.handleToolsCall(ctx, req.Params)
	default:
		return MCPResponse{
			Error: &MCPError{
				Code:    -32601,
				Message: fmt.Sprintf("Method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize handles the initialize request
func (s *MCPServer) handleInitialize(ctx context.Context) MCPResponse {
	info := ServerInfo{
		Name:    serverName,
		Version: serverVersion,
	}

	result, _ := json.Marshal(info)
	return MCPResponse{Result: result}
}

// handleToolsList returns the list of available tools
func (s *MCPServer) handleToolsList(ctx context.Context) MCPResponse {
	tools := []ToolInfo{
		{
			Name:        "list_clusters",
			Description: "List OCM managed clusters with optional label filtering. You can filter by specific labels (e.g., region=us-west, environment=production) or use Kubernetes label selectors.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"labelSelector": map[string]interface{}{
						"type":        "string",
						"description": "Kubernetes label selector (e.g., 'environment=production,region=us-west')",
					},
					"labels": map[string]interface{}{
						"type":        "object",
						"description": "Map of label key-value pairs to filter clusters",
						"additionalProperties": map[string]interface{}{
							"type": "string",
						},
					},
				},
			},
		},
		{
			Name:        "get_cluster",
			Description: "Get details of a specific OCM managed cluster by name",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the cluster to retrieve",
					},
				},
				"required": []string{"name"},
			},
		},
	}

	result, _ := json.Marshal(map[string]interface{}{"tools": tools})
	return MCPResponse{Result: result}
}

// handleToolsCall handles tool execution requests
func (s *MCPServer) handleToolsCall(ctx context.Context, params json.RawMessage) MCPResponse {
	var toolCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal(params, &toolCall); err != nil {
		return MCPResponse{
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
		}
	}

	switch toolCall.Name {
	case "list_clusters":
		return s.listClusters(ctx, toolCall.Arguments)
	case "get_cluster":
		return s.getCluster(ctx, toolCall.Arguments)
	default:
		return MCPResponse{
			Error: &MCPError{
				Code:    -32601,
				Message: fmt.Sprintf("Tool not found: %s", toolCall.Name),
			},
		}
	}
}

// listClusters lists managed clusters with optional label filtering
func (s *MCPServer) listClusters(ctx context.Context, arguments json.RawMessage) MCPResponse {
	var params ListClustersParams
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &params); err != nil {
			return MCPResponse{
				Error: &MCPError{
					Code:    -32602,
					Message: fmt.Sprintf("Invalid arguments: %v", err),
				},
			}
		}
	}

	// Build label selector
	listOpts := metav1.ListOptions{}
	if params.LabelSelector != "" {
		listOpts.LabelSelector = params.LabelSelector
	} else if len(params.Labels) > 0 {
		selector := labels.Set(params.Labels).AsSelector()
		listOpts.LabelSelector = selector.String()
	}

	clusters, err := s.clusterClient.ClusterV1().ManagedClusters().List(ctx, listOpts)
	if err != nil {
		return MCPResponse{
			Error: &MCPError{
				Code:    -32603,
				Message: fmt.Sprintf("Failed to list clusters: %v", err),
			},
		}
	}

	clusterInfos := make([]ClusterInfo, 0, len(clusters.Items))
	for _, cluster := range clusters.Items {
		clusterInfos = append(clusterInfos, convertToClusterInfo(&cluster))
	}

	result, _ := json.Marshal(map[string]interface{}{
		"clusters": clusterInfos,
		"count":    len(clusterInfos),
	})
	return MCPResponse{Result: result}
}

// getCluster retrieves a specific cluster by name
func (s *MCPServer) getCluster(ctx context.Context, arguments json.RawMessage) MCPResponse {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(arguments, &params); err != nil {
		return MCPResponse{
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid arguments: %v", err),
			},
		}
	}

	if params.Name == "" {
		return MCPResponse{
			Error: &MCPError{
				Code:    -32602,
				Message: "Cluster name is required",
			},
		}
	}

	cluster, err := s.clusterClient.ClusterV1().ManagedClusters().Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return MCPResponse{
			Error: &MCPError{
				Code:    -32603,
				Message: fmt.Sprintf("Failed to get cluster: %v", err),
			},
		}
	}

	info := convertToClusterInfo(cluster)
	result, _ := json.Marshal(info)
	return MCPResponse{Result: result}
}

// convertToClusterInfo converts a ManagedCluster to ClusterInfo
func convertToClusterInfo(cluster *clusterv1.ManagedCluster) ClusterInfo {
	info := ClusterInfo{
		Name:          cluster.Name,
		Labels:        cluster.Labels,
		ClusterClaims: make(map[string]string),
		Accepted:      cluster.Spec.HubAcceptsClient,
		Status:        "Unknown",
	}

	// Extract cluster claims
	for _, claim := range cluster.Status.ClusterClaims {
		info.ClusterClaims[claim.Name] = claim.Value
		if claim.Name == "kubeversion.open-cluster-management.io" {
			info.KubeVersion = claim.Value
		}
	}

	// Extract capacity and allocatable resources
	if cluster.Status.Capacity != nil {
		info.Capacity = make(map[string]string)
		for k, v := range cluster.Status.Capacity {
			info.Capacity[string(k)] = v.String()
		}
	}
	if cluster.Status.Allocatable != nil {
		info.Allocatable = make(map[string]string)
		for k, v := range cluster.Status.Allocatable {
			info.Allocatable[string(k)] = v.String()
		}
	}

	// Determine cluster status from conditions
	for _, condition := range cluster.Status.Conditions {
		if condition.Type == clusterv1.ManagedClusterConditionAvailable {
			if condition.Status == metav1.ConditionTrue {
				info.Status = "Available"
			} else {
				info.Status = "Unavailable"
			}
			break
		}
	}

	return info
}

// Run starts the MCP server
func (s *MCPServer) Run(ctx context.Context) error {
	klog.InfoS("Starting MCP server", "name", serverName, "version", serverVersion)

	// Read JSON-RPC requests from stdin and write responses to stdout
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			var req MCPRequest
			if err := decoder.Decode(&req); err != nil {
				klog.ErrorS(err, "Failed to decode request")
				return err
			}

			klog.V(2).InfoS("Received request", "method", req.Method)
			resp := s.HandleRequest(ctx, req)

			if err := encoder.Encode(resp); err != nil {
				klog.ErrorS(err, "Failed to encode response")
				return err
			}
		}
	}
}

func main() {
	var kubeconfig string
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file")
	klog.InitFlags(nil)
	flag.Parse()

	server, err := NewMCPServer(kubeconfig)
	if err != nil {
		klog.ErrorS(err, "Failed to create MCP server")
		os.Exit(1)
	}

	ctx := context.Background()
	if err := server.Run(ctx); err != nil {
		klog.ErrorS(err, "Server error")
		os.Exit(1)
	}
}
