package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"k8s.io/klog/v2"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"

	"open-cluster-management.io/ocm/mcp/pkg/cluster"
	"open-cluster-management.io/ocm/mcp/pkg/placement"
)

const (
	ServerName    = "ocm-cluster-mcp"
	ServerVersion = "0.1.0"
)

// MCPServer represents the MCP server for OCM cluster operations
type MCPServer struct {
	clusterHandler   *cluster.Handler
	placementHandler *placement.Handler
	sseConnections   map[string]http.ResponseWriter
	sseConnMutex     sync.RWMutex
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

// ToolInfo represents an MCP tool
type ToolInfo struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	InputSchema interface{} `json:"inputSchema"`
}

// NewMCPServer creates a new MCP server
func NewMCPServer(clusterClient clusterclientset.Interface) *MCPServer {
	return &MCPServer{
		clusterHandler:   cluster.NewHandler(clusterClient),
		placementHandler: placement.NewHandler(clusterClient),
		sseConnections:   make(map[string]http.ResponseWriter),
	}
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
		result, _ := json.Marshal(map[string]interface{}{"resources": []interface{}{}})
		resp.Result = result
	case "prompts/list":
		result, _ := json.Marshal(map[string]interface{}{"prompts": []interface{}{}})
		resp.Result = result
	case "notifications/initialized":
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
			Name:    ServerName,
			Version: ServerVersion,
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
		{
			Name:        "generate_placement",
			Description: "Generate an OCM Placement YAML from natural language description. Supports filtering by environment, region, cloud provider, OpenShift version, and resource capacity.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name of the Placement resource",
					},
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Namespace for the Placement resource",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Natural language description of placement requirements (e.g., 'deploy to all production clusters in us-east with OpenShift >= 4.16')",
					},
				},
				"required": []string{"name", "namespace", "description"},
			},
		},
		{
			Name:        "dryrun_placement",
			Description: "Perform a dry run of a Placement to show which clusters would be selected. Returns PlacementDecision-like results with cluster names and selection reasons. You can provide either a Placement YAML or a natural language description.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"placementYAML": map[string]interface{}{
						"type":        "string",
						"description": "Placement YAML to evaluate (optional if description is provided)",
					},
					"description": map[string]interface{}{
						"type":        "string",
						"description": "Natural language description of placement requirements (optional if placementYAML is provided)",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Name for the placement (used with description)",
					},
					"namespace": map[string]interface{}{
						"type":        "string",
						"description": "Namespace for the placement (used with description)",
					},
				},
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
	case "generate_placement":
		return s.generatePlacement(ctx, id, toolCall.Arguments)
	case "dryrun_placement":
		return s.dryrunPlacement(ctx, id, toolCall.Arguments)
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
	var params cluster.ListClustersParams
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

	clusterInfos, err := s.clusterHandler.ListClusters(ctx, params)
	if err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
	}

	result, _ := cluster.MarshalResult(map[string]interface{}{
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

	info, err := s.clusterHandler.GetCluster(ctx, params.Name)
	if err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
	}

	result, _ := cluster.MarshalResult(info)
	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// generatePlacement generates a Placement YAML from natural language description
func (s *MCPServer) generatePlacement(ctx context.Context, id interface{}, arguments json.RawMessage) MCPResponse {
	var params placement.GeneratePlacementParams
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

	yamlOutput, err := s.placementHandler.GeneratePlacement(ctx, params)
	if err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
	}

	result, _ := placement.MarshalResult(map[string]interface{}{
		"yaml": yamlOutput,
	})

	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// dryrunPlacement performs a dry run of a Placement to show which clusters would be selected
func (s *MCPServer) dryrunPlacement(ctx context.Context, id interface{}, arguments json.RawMessage) MCPResponse {
	var params placement.DryRunPlacementParams
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

	dryRunResult, err := s.placementHandler.DryRunPlacement(ctx, params)
	if err != nil {
		return MCPResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error: &MCPError{
				Code:    -32603,
				Message: err.Error(),
			},
		}
	}

	result, _ := placement.MarshalResult(dryRunResult)

	return MCPResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

// Run starts the MCP server
func (s *MCPServer) Run(ctx context.Context) error {
	klog.InfoS("Starting MCP server", "name", ServerName, "version", ServerVersion)

	httpMode := os.Getenv("HTTP_MODE")
	if httpMode == "true" {
		return s.RunHTTP(ctx)
	}

	return s.RunStdio(ctx)
}

// RunStdio runs the MCP server in stdio mode
func (s *MCPServer) RunStdio(ctx context.Context) error {
	klog.InfoS("Running in stdio mode")

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

	http.HandleFunc("/sse", s.handleSSEConnection)
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

// handleSSEConnection handles SSE connection establishment
func (s *MCPServer) handleSSEConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strconv.FormatInt(time.Now().UnixNano(), 10)
	klog.V(2).InfoS("New SSE connection", "sessionID", sessionID)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	s.sseConnMutex.Lock()
	s.sseConnections[sessionID] = w
	s.sseConnMutex.Unlock()

	fmt.Fprintf(w, "event: endpoint\ndata: /messages/?session_id=%s\n\n", sessionID)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

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

	s.sseConnMutex.RLock()
	sseWriter, ok := s.sseConnections[sessionID]
	s.sseConnMutex.RUnlock()

	if !ok {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	var req MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		klog.ErrorS(err, "Failed to decode request")
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Accepted"))

	resp := s.HandleRequest(r.Context(), req)

	if req.Method == "notifications/initialized" {
		klog.V(2).InfoS("Handled notification, no response needed", "sessionID", sessionID)
		return
	}

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
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    "ok",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}
