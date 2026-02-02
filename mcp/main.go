package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

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
	clusterClient  clusterclientset.Interface
	sseConnections map[string]http.ResponseWriter
	sseConnMutex   sync.RWMutex
}

// MCPRequest represents an incoming MCP request
type MCPRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// MCPResponse represents an MCP response
type MCPResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

// MCPError represents an MCP error
type MCPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ServerInfo represents server information for initialize response
type ServerInfo struct {
	ProtocolVersion string           `json:"protocolVersion"`
	Capabilities    Capabilities     `json:"capabilities"`
	ServerInfo      ServerInfoDetail `json:"serverInfo"`
}

// Capabilities represents server capabilities
type Capabilities struct {
	Tools ToolsCapability `json:"tools"`
}

// ToolsCapability represents tools capability
type ToolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

// ServerInfoDetail represents detailed server information
type ServerInfoDetail struct {
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
		clusterClient:  clusterClient,
		sseConnections: make(map[string]http.ResponseWriter),
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
	var resp MCPResponse
	resp.JSONRPC = "2.0"
	resp.ID = req.ID

	switch req.Method {
	case "initialize":
		resp = s.handleInitialize(ctx, req.ID)
	case "tools/list":
		resp = s.handleToolsList(ctx, req.ID)
	case "tools/call":
		resp = s.handleToolsCall(ctx, req.ID, req.Params)
	case "resources/list":
		// We don't provide any resources
		result, _ := json.Marshal(map[string]interface{}{"resources": []interface{}{}})
		resp.Result = result
	case "prompts/list":
		// We don't provide any prompts
		result, _ := json.Marshal(map[string]interface{}{"prompts": []interface{}{}})
		resp.Result = result
	case "notifications/initialized":
		// This is a notification, not a request, so we don't send a response
		return MCPResponse{}
	default:
		resp.Error = &MCPError{
			Code:    -32601,
			Message: fmt.Sprintf("Method not found: %s", req.Method),
		}
	}

	return resp
}

// handleInitialize handles the initialize request
func (s *MCPServer) handleInitialize(ctx context.Context, id interface{}) MCPResponse {
	info := ServerInfo{
		ProtocolVersion: "2025-06-18",
		Capabilities: Capabilities{
			Tools: ToolsCapability{
				ListChanged: true,
			},
		},
		ServerInfo: ServerInfoDetail{
			Name:    serverName,
			Version: serverVersion,
		},
	}

	result, _ := json.Marshal(info)
	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// handleToolsList returns the list of available tools
func (s *MCPServer) handleToolsList(ctx context.Context, id interface{}) MCPResponse {
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
	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// handleToolsCall handles tool execution requests
func (s *MCPServer) handleToolsCall(ctx context.Context, id interface{}, params json.RawMessage) MCPResponse {
	var toolCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}

	if err := json.Unmarshal(params, &toolCall); err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid params: %v", err),
			},
		}
	}

	switch toolCall.Name {
	case "list_clusters":
		return s.listClusters(ctx, id, toolCall.Arguments)
	case "get_cluster":
		return s.getCluster(ctx, id, toolCall.Arguments)
	default:
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32601,
				Message: fmt.Sprintf("Tool not found: %s", toolCall.Name),
			},
		}
	}
}

// listClusters lists managed clusters with optional label filtering
func (s *MCPServer) listClusters(ctx context.Context, id interface{}, arguments json.RawMessage) MCPResponse {
	var params ListClustersParams
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &params); err != nil {
			return MCPResponse{
				JSONRPC: "2.0",
				ID:      id,
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
			JSONRPC: "2.0",
			ID:      id,
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
	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// getCluster retrieves a specific cluster by name
func (s *MCPServer) getCluster(ctx context.Context, id interface{}, arguments json.RawMessage) MCPResponse {
	var params struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(arguments, &params); err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32602,
				Message: fmt.Sprintf("Invalid arguments: %v", err),
			},
		}
	}

	if params.Name == "" {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32602,
				Message: "Cluster name is required",
			},
		}
	}

	cluster, err := s.clusterClient.ClusterV1().ManagedClusters().Get(ctx, params.Name, metav1.GetOptions{})
	if err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32603,
				Message: fmt.Sprintf("Failed to get cluster: %v", err),
			},
		}
	}

	info := convertToClusterInfo(cluster)
	result, _ := json.Marshal(info)
	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
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

	// Check if we should run as HTTP server (for OpenShift route)
	httpMode := os.Getenv("HTTP_MODE")
	if httpMode == "true" {
		return s.RunHTTP(ctx)
	}

	// Default stdio mode
	return s.RunStdio(ctx)
}

// RunStdio runs the MCP server in stdio mode
func (s *MCPServer) RunStdio(ctx context.Context) error {
	klog.InfoS("Running in stdio mode")

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

// RunHTTP runs the MCP server in HTTP mode for remote access
func (s *MCPServer) RunHTTP(ctx context.Context) error {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	klog.InfoS("Running in HTTP mode", "port", port)

	http.HandleFunc("/mcp", s.handleSSEConnection)
	http.HandleFunc("/messages/", s.handleSSEMessage)
	http.HandleFunc("/health", s.handleHealth)

	server := &http.Server{
		Addr: ":" + port,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	return server.ListenAndServe()
}

// handleHTTPRequest handles HTTP POST requests with MCP JSON-RPC
func (s *MCPServer) handleHTTPRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		klog.ErrorS(err, "Failed to decode request")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	klog.V(2).InfoS("Received HTTP request", "method", req.Method)
	resp := s.HandleRequest(r.Context(), req)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		klog.ErrorS(err, "Failed to encode response")
	}
}

// handleSSEConnection handles SSE connection establishment
func (s *MCPServer) handleSSEConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Generate session ID
	sessionID := strconv.FormatInt(time.Now().UnixNano(), 10)

	klog.V(2).InfoS("New SSE connection", "sessionID", sessionID)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Store connection
	s.sseConnMutex.Lock()
	s.sseConnections[sessionID] = w
	s.sseConnMutex.Unlock()

	// Send endpoint message
	fmt.Fprintf(w, "event: endpoint\ndata: /messages/?session_id=%s\n\n", sessionID)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Keep connection alive with heartbeat
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			s.sseConnMutex.Lock()
			delete(s.sseConnections, sessionID)
			s.sseConnMutex.Unlock()
			klog.V(2).InfoS("SSE connection closed", "sessionID", sessionID)
			return
		case <-ticker.C:
			fmt.Fprintf(w, ": ping - %s\n\n", time.Now().Format(time.RFC3339))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}
}

// handleSSEMessage handles messages sent to SSE connections
func (s *MCPServer) handleSSEMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "Invalid session ID", http.StatusBadRequest)
		return
	}

	klog.V(2).InfoS("Received SSE message", "sessionID", sessionID)

	// Get SSE connection
	s.sseConnMutex.RLock()
	sseWriter, ok := s.sseConnections[sessionID]
	s.sseConnMutex.RUnlock()

	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Decode MCP request
	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		klog.ErrorS(err, "Failed to decode request")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// Accept the message immediately (like the TypeScript server)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Accepted"))

	// Process the request
	resp := s.HandleRequest(r.Context(), req)

	// Don't send a response for notifications
	if req.Method == "notifications/initialized" {
		klog.V(2).InfoS("Handled notification, no response needed", "sessionID", sessionID)
		return
	}

	// Send response via SSE
	respJSON, err := json.Marshal(resp)
	if err != nil {
		klog.ErrorS(err, "Failed to marshal response")
		return
	}

	fmt.Fprintf(sseWriter, "event: message\ndata: %s\n\n", string(respJSON))
	if f, ok := sseWriter.(http.Flusher); ok {
		f.Flush()
	}

	klog.V(2).InfoS("Sent SSE response", "sessionID", sessionID, "method", req.Method)
}

// handleHealth handles health check requests
func (s *MCPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"name":   serverName,
		"version": serverVersion,
	})
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
