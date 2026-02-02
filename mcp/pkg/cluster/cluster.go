package cluster

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
)

// Handler handles cluster-related MCP operations
type Handler struct {
	ClusterClient clusterclientset.Interface
}

// NewHandler creates a new cluster handler
func NewHandler(clusterClient clusterclientset.Interface) *Handler {
	return &Handler{
		ClusterClient: clusterClient,
	}
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

// ListClusters lists managed clusters with optional label filtering
func (h *Handler) ListClusters(ctx context.Context, params ListClustersParams) ([]ClusterInfo, error) {
	// Build label selector
	listOpts := metav1.ListOptions{}
	if params.LabelSelector != "" {
		listOpts.LabelSelector = params.LabelSelector
	} else if len(params.Labels) > 0 {
		selector := labels.Set(params.Labels).AsSelector()
		listOpts.LabelSelector = selector.String()
	}

	clusters, err := h.ClusterClient.ClusterV1().ManagedClusters().List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	clusterInfos := make([]ClusterInfo, 0, len(clusters.Items))
	for _, cluster := range clusters.Items {
		clusterInfos = append(clusterInfos, convertToClusterInfo(&cluster))
	}

	return clusterInfos, nil
}

// GetCluster retrieves a specific cluster by name
func (h *Handler) GetCluster(ctx context.Context, name string) (*ClusterInfo, error) {
	if name == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	cluster, err := h.ClusterClient.ClusterV1().ManagedClusters().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster: %v", err)
	}

	info := convertToClusterInfo(cluster)
	return &info, nil
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

// MarshalResult marshals cluster data into MCP content block format
func MarshalResult(data interface{}) (json.RawMessage, error) {
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	result, err := json.Marshal(map[string]interface{}{
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": string(dataJSON),
			},
		},
	})
	return result, err
}
