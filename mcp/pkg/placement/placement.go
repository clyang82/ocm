package placement

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clusterclientset "open-cluster-management.io/api/client/cluster/clientset/versioned"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	clusterv1beta1 "open-cluster-management.io/api/cluster/v1beta1"
	"sigs.k8s.io/yaml"
)

// Handler handles placement-related MCP operations
type Handler struct {
	ClusterClient clusterclientset.Interface
}

// NewHandler creates a new placement handler
func NewHandler(clusterClient clusterclientset.Interface) *Handler {
	return &Handler{
		ClusterClient: clusterClient,
	}
}

// GeneratePlacementParams represents parameters for generating placement
type GeneratePlacementParams struct {
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	Description string `json:"description"`
}

// DryRunPlacementParams represents parameters for dry-running placement
type DryRunPlacementParams struct {
	PlacementYAML string `json:"placementYAML,omitempty"`
	Description   string `json:"description,omitempty"`
	Name          string `json:"name,omitempty"`
	Namespace     string `json:"namespace,omitempty"`
}

// DryRunPlacementResult represents the result of a placement dry run
// This mimics the PlacementDecision structure from OCM
type DryRunPlacementResult struct {
	Decisions    []ClusterDecision `json:"decisions"`
	TotalMatched int               `json:"totalMatched"`
	Summary      string            `json:"summary"`
}

// ClusterDecision represents a decision for a selected cluster
// This matches clusterapiv1beta1.ClusterDecision
type ClusterDecision struct {
	ClusterName string `json:"clusterName"`
	Reason      string `json:"reason"`
}

// GeneratePlacement generates a Placement YAML based on natural language description
func (h *Handler) GeneratePlacement(ctx context.Context, params GeneratePlacementParams) (string, error) {
	if params.Name == "" {
		return "", fmt.Errorf("placement name is required")
	}
	if params.Namespace == "" {
		return "", fmt.Errorf("placement namespace is required")
	}
	if params.Description == "" {
		return "", fmt.Errorf("description is required")
	}

	// Parse the description to extract requirements
	requirements := parseRequirements(params.Description)

	// Build the Placement object
	placement := &clusterv1beta1.Placement{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "cluster.open-cluster-management.io/v1beta1",
			Kind:       "Placement",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.Name,
			Namespace: params.Namespace,
		},
		Spec: buildPlacementSpec(requirements),
	}

	// Convert to YAML
	yamlBytes, err := yaml.Marshal(placement)
	if err != nil {
		return "", fmt.Errorf("failed to marshal placement to YAML: %v", err)
	}

	return string(yamlBytes), nil
}

// DryRunPlacement evaluates which clusters would be selected by a Placement without creating it
func (h *Handler) DryRunPlacement(ctx context.Context, params DryRunPlacementParams) (*DryRunPlacementResult, error) {
	var placement *clusterv1beta1.Placement

	// Parse placement from YAML or description
	if params.PlacementYAML != "" {
		placement = &clusterv1beta1.Placement{}
		if err := yaml.Unmarshal([]byte(params.PlacementYAML), placement); err != nil {
			return nil, fmt.Errorf("failed to unmarshal placement YAML: %v", err)
		}
	} else if params.Description != "" {
		// Generate placement from description
		if params.Name == "" {
			params.Name = "dryrun-placement"
		}
		if params.Namespace == "" {
			params.Namespace = "default"
		}
		requirements := parseRequirements(params.Description)
		placement = &clusterv1beta1.Placement{
			ObjectMeta: metav1.ObjectMeta{
				Name:      params.Name,
				Namespace: params.Namespace,
			},
			Spec: buildPlacementSpec(requirements),
		}
	} else {
		return nil, fmt.Errorf("either placementYAML or description must be provided")
	}

	// Get all clusters
	clusters, err := h.ClusterClient.ClusterV1().ManagedClusters().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %v", err)
	}

	// Filter and evaluate clusters
	decisions := []ClusterDecision{}

	for _, cluster := range clusters.Items {
		// Skip clusters in terminating state
		if !cluster.DeletionTimestamp.IsZero() {
			continue
		}

		// Check if cluster is tolerated (check taints)
		tolerated, taintReason := isClusterTolerated(&cluster, placement.Spec.Tolerations)
		if !tolerated {
			continue
		}

		// If no predicates, select all tolerated clusters
		if len(placement.Spec.Predicates) == 0 {
			reason := "No predicates specified, all clusters match"
			if taintReason != "" {
				reason = taintReason
			}
			decisions = append(decisions, ClusterDecision{
				ClusterName: cluster.Name,
				Reason:      reason,
			})
		} else {
			// Match clusters against predicates (predicates are ORed)
			if matched, reason := matchClusterWithPredicates(&cluster, placement.Spec.Predicates); matched {
				decisions = append(decisions, ClusterDecision{
					ClusterName: cluster.Name,
					Reason:      reason,
				})
			}
		}
	}

	// Apply numberOfClusters limit if specified
	if placement.Spec.NumberOfClusters != nil {
		limit := int(*placement.Spec.NumberOfClusters)
		if len(decisions) > limit {
			decisions = decisions[:limit]
		}
	}

	summary := fmt.Sprintf("Selected %d out of %d total clusters", len(decisions), len(clusters.Items))

	return &DryRunPlacementResult{
		Decisions:    decisions,
		TotalMatched: len(decisions),
		Summary:      summary,
	}, nil
}

// isClusterTolerated checks if a cluster's taints are tolerated by the placement
// Returns (tolerated, reason)
func isClusterTolerated(cluster *clusterv1.ManagedCluster, tolerations []clusterv1beta1.Toleration) (bool, string) {
	// If cluster has no taints, it's tolerated
	if len(cluster.Spec.Taints) == 0 {
		return true, ""
	}

	// Check each taint
	for _, taint := range cluster.Spec.Taints {
		// PreferNoSelect doesn't block selection
		if taint.Effect == clusterv1.TaintEffectPreferNoSelect {
			continue
		}

		// Check if this taint is tolerated
		tolerated := false
		for _, toleration := range tolerations {
			if isTaintTolerated(taint, toleration) {
				tolerated = true
				break
			}
		}

		// If not tolerated and effect is NoSelect, cluster cannot be selected
		if !tolerated && taint.Effect == clusterv1.TaintEffectNoSelect {
			return false, fmt.Sprintf("Cluster has untolerated taint: %s", taint.Key)
		}
	}

	return true, ""
}

// isTaintTolerated checks if a specific taint is tolerated by a toleration
func isTaintTolerated(taint clusterv1.Taint, toleration clusterv1beta1.Toleration) bool {
	// Check effect matches if specified
	if len(toleration.Effect) > 0 && toleration.Effect != taint.Effect {
		return false
	}

	// Check key matches if specified
	if len(toleration.Key) > 0 && toleration.Key != taint.Key {
		return false
	}

	// Check operator and value
	switch toleration.Operator {
	case "", clusterv1beta1.TolerationOpEqual:
		return toleration.Value == taint.Value
	case clusterv1beta1.TolerationOpExists:
		return true
	default:
		return false
	}
}

// matchClusterWithPredicates checks if a cluster matches any of the predicates (ORed)
func matchClusterWithPredicates(cluster *clusterv1.ManagedCluster, predicates []clusterv1beta1.ClusterPredicate) (bool, string) {
	for i, predicate := range predicates {
		if matched, _ := matchClusterWithPredicate(cluster, &predicate); matched {
			return true, fmt.Sprintf("Matched predicate %d", i)
		}
	}
	return false, "No predicates matched"
}

// matchClusterWithPredicate checks if a cluster matches a single predicate
// All selectors within a predicate are ANDed
func matchClusterWithPredicate(cluster *clusterv1.ManagedCluster, predicate *clusterv1beta1.ClusterPredicate) (bool, string) {
	selector := predicate.RequiredClusterSelector

	// Check label selector
	if len(selector.LabelSelector.MatchExpressions) > 0 || len(selector.LabelSelector.MatchLabels) > 0 {
		labelSelector, err := metav1.LabelSelectorAsSelector(&selector.LabelSelector)
		if err != nil {
			return false, fmt.Sprintf("Invalid label selector: %v", err)
		}
		if !labelSelector.Matches(labels.Set(cluster.Labels)) {
			return false, "Label selector did not match"
		}
	}

	// Check claim selector with custom operator support
	if len(selector.ClaimSelector.MatchExpressions) > 0 {
		clusterClaims := getClusterClaims(cluster)
		for _, requirement := range selector.ClaimSelector.MatchExpressions {
			if !matchClaimRequirement(clusterClaims, requirement) {
				return false, fmt.Sprintf("Claim requirement not met: %s %s", requirement.Key, requirement.Operator)
			}
		}
	}

	// CEL selector is not supported in dry run for now
	if len(selector.CelSelector.CelExpressions) > 0 {
		return false, "CEL selector is not supported in dry run"
	}

	return true, "All selectors matched"
}

// getClusterClaims extracts cluster claims into a map
func getClusterClaims(cluster *clusterv1.ManagedCluster) map[string]string {
	claims := make(map[string]string)
	for _, claim := range cluster.Status.ClusterClaims {
		claims[claim.Name] = claim.Value
	}
	return claims
}

// matchClaimRequirement checks if a claim requirement is satisfied
// Supports standard Kubernetes operators (In, NotIn, Exists, DoesNotExist)
// and OCM custom operators (Gt, Lt) for version comparison
func matchClaimRequirement(claims map[string]string, requirement metav1.LabelSelectorRequirement) bool {
	claimValue, exists := claims[requirement.Key]

	switch requirement.Operator {
	case metav1.LabelSelectorOpIn:
		if !exists {
			return false
		}
		for _, value := range requirement.Values {
			if claimValue == value {
				return true
			}
		}
		return false

	case metav1.LabelSelectorOpNotIn:
		if !exists {
			return true
		}
		for _, value := range requirement.Values {
			if claimValue == value {
				return false
			}
		}
		return true

	case metav1.LabelSelectorOpExists:
		return exists

	case metav1.LabelSelectorOpDoesNotExist:
		return !exists

	case metav1.LabelSelectorOperator("Gt"):
		// Greater than comparison for version strings
		if !exists || len(requirement.Values) == 0 {
			return false
		}
		// Simple string comparison works for version format like "4.16"
		return claimValue > requirement.Values[0]

	case metav1.LabelSelectorOperator("Lt"):
		// Less than comparison
		if !exists || len(requirement.Values) == 0 {
			return false
		}
		return claimValue < requirement.Values[0]

	default:
		// Unknown operator
		return false
	}
}

// Requirements represents parsed placement requirements
type Requirements struct {
	Environment      string   // e.g., "production", "staging"
	Regions          []string // e.g., ["us-east", "us-west"]
	MinCPU           string   // e.g., "8"
	MinMemory        string   // e.g., "16Gi"
	MinVersion       string   // e.g., "4.16"
	CloudProvider    string   // e.g., "AWS", "Azure", "GCP"
	CustomLabels     map[string]string
	CustomClaims     map[string]string
	NumberOfClusters *int32
}

// parseRequirements parses natural language description into structured requirements
func parseRequirements(description string) Requirements {
	req := Requirements{
		CustomLabels: make(map[string]string),
		CustomClaims: make(map[string]string),
	}

	desc := strings.ToLower(description)

	// Parse environment
	if strings.Contains(desc, "production") || strings.Contains(desc, "prod") {
		req.Environment = "production"
	} else if strings.Contains(desc, "staging") || strings.Contains(desc, "stage") {
		req.Environment = "staging"
	} else if strings.Contains(desc, "development") || strings.Contains(desc, "dev") {
		req.Environment = "development"
	}

	// Parse regions
	regionPatterns := []string{
		"us-east", "us-west", "us-central",
		"eu-west", "eu-central", "eu-north",
		"ap-southeast", "ap-northeast", "ap-south",
	}
	for _, region := range regionPatterns {
		if strings.Contains(desc, region) {
			req.Regions = append(req.Regions, region)
		}
	}

	// Parse cloud provider
	if strings.Contains(desc, "aws") || strings.Contains(desc, "amazon") {
		req.CloudProvider = "AWS"
	} else if strings.Contains(desc, "azure") {
		req.CloudProvider = "Azure"
	} else if strings.Contains(desc, "gcp") || strings.Contains(desc, "google") {
		req.CloudProvider = "GCP"
	}

	// Parse OpenShift version (e.g., "OpenShift ≥ 4.16" or "OpenShift >= 4.16")
	versionRegex := regexp.MustCompile(`openshift\s*[≥>=]+\s*(\d+\.\d+)`)
	if matches := versionRegex.FindStringSubmatch(desc); len(matches) > 1 {
		req.MinVersion = matches[1]
	}

	// Parse capacity requirements
	cpuRegex := regexp.MustCompile(`(\d+)\s*(?:cpu|cores|cpus)`)
	if matches := cpuRegex.FindStringSubmatch(desc); len(matches) > 1 {
		req.MinCPU = matches[1]
	}

	memoryRegex := regexp.MustCompile(`(\d+)\s*(?:gi|gb|gib)`)
	if matches := memoryRegex.FindStringSubmatch(desc); len(matches) > 1 {
		req.MinMemory = matches[1] + "Gi"
	}

	// Parse number of clusters
	numRegex := regexp.MustCompile(`(\d+)\s*clusters?`)
	if matches := numRegex.FindStringSubmatch(desc); len(matches) > 1 {
		if num, err := strconv.ParseInt(matches[1], 10, 32); err == nil {
			n := int32(num)
			req.NumberOfClusters = &n
		}
	}

	return req
}

// buildPlacementSpec builds a PlacementSpec from requirements
func buildPlacementSpec(req Requirements) clusterv1beta1.PlacementSpec {
	spec := clusterv1beta1.PlacementSpec{
		Predicates: []clusterv1beta1.ClusterPredicate{},
	}

	// Set number of clusters if specified
	if req.NumberOfClusters != nil {
		spec.NumberOfClusters = req.NumberOfClusters
	}

	// Build cluster selector
	predicate := clusterv1beta1.ClusterPredicate{
		RequiredClusterSelector: clusterv1beta1.ClusterSelector{},
	}

	// Label selector for basic attributes
	labelRequirements := []metav1.LabelSelectorRequirement{}

	// Environment label
	if req.Environment != "" {
		labelRequirements = append(labelRequirements, metav1.LabelSelectorRequirement{
			Key:      "environment",
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{req.Environment},
		})
	}

	// Cloud provider
	if req.CloudProvider != "" {
		labelRequirements = append(labelRequirements, metav1.LabelSelectorRequirement{
			Key:      "cloud",
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{req.CloudProvider},
		})
	}

	// Custom labels
	for k, v := range req.CustomLabels {
		labelRequirements = append(labelRequirements, metav1.LabelSelectorRequirement{
			Key:      k,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{v},
		})
	}

	if len(labelRequirements) > 0 {
		predicate.RequiredClusterSelector.LabelSelector = metav1.LabelSelector{
			MatchExpressions: labelRequirements,
		}
	}

	// Cluster claim selector for version, region, and capacity
	claimRequirements := []metav1.LabelSelectorRequirement{}

	// Region claim
	if len(req.Regions) > 0 {
		claimRequirements = append(claimRequirements, metav1.LabelSelectorRequirement{
			Key:      "region.open-cluster-management.io",
			Operator: metav1.LabelSelectorOpIn,
			Values:   req.Regions,
		})
	}

	// OpenShift version claim (using Gt operator for version comparison)
	if req.MinVersion != "" {
		claimRequirements = append(claimRequirements, metav1.LabelSelectorRequirement{
			Key:      "version.openshift.io",
			Operator: metav1.LabelSelectorOperator("Gt"),
			Values:   []string{req.MinVersion},
		})
	}

	// Custom claims
	for k, v := range req.CustomClaims {
		claimRequirements = append(claimRequirements, metav1.LabelSelectorRequirement{
			Key:      k,
			Operator: metav1.LabelSelectorOpIn,
			Values:   []string{v},
		})
	}

	if len(claimRequirements) > 0 {
		predicate.RequiredClusterSelector.ClaimSelector = clusterv1beta1.ClusterClaimSelector{
			MatchExpressions: claimRequirements,
		}
	}

	// Add predicate if it has any selectors
	if len(labelRequirements) > 0 || len(claimRequirements) > 0 {
		spec.Predicates = append(spec.Predicates, predicate)
	}

	return spec
}

// MarshalResult marshals placement data into MCP content block format
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
