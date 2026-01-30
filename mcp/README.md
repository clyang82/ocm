# OCM Cluster MCP Server

A Model Context Protocol (MCP) server for listing and querying Open Cluster Management (OCM) managed clusters with label-based filtering.

## Features

- **List Clusters**: List all managed clusters with optional label filtering
- **Get Cluster**: Retrieve detailed information about a specific cluster
- **Label Filtering**: Filter clusters by labels (e.g., region, environment, etc.)
- **Cluster Information**: Returns cluster metadata, status, capacity, and claims

## Installation

```bash
# From the repository root
cd mcp
go build -o ocm-cluster-mcp
```

## Usage

### Running the Server

```bash
# Use default kubeconfig (~/.kube/config)
./ocm-cluster-mcp

# Specify a kubeconfig file
./ocm-cluster-mcp -kubeconfig=/path/to/kubeconfig

# Enable verbose logging
./ocm-cluster-mcp -v=2
```

### MCP Tools

The server exposes the following tools:

#### 1. list_clusters

List OCM managed clusters with optional label filtering.

**Parameters:**
- `labelSelector` (optional): Kubernetes label selector string (e.g., `"environment=production,region=us-west"`)
- `labels` (optional): Map of label key-value pairs to filter clusters

**Examples:**

```json
{
  "name": "list_clusters",
  "arguments": {
    "labelSelector": "environment=production"
  }
}
```

```json
{
  "name": "list_clusters",
  "arguments": {
    "labels": {
      "region": "us-west",
      "environment": "production"
    }
  }
}
```

**Response:**
```json
{
  "clusters": [
    {
      "name": "cluster1",
      "labels": {
        "region": "us-west",
        "environment": "production",
        "cluster.open-cluster-management.io/clusterset": "default"
      },
      "clusterClaims": {
        "id.k8s.io": "cluster1-id",
        "kubeversion.open-cluster-management.io": "v1.24.0"
      },
      "status": "Available",
      "accepted": true,
      "capacity": {
        "cpu": "16",
        "memory": "64Gi"
      },
      "allocatable": {
        "cpu": "15",
        "memory": "60Gi"
      },
      "kubeVersion": "v1.24.0"
    }
  ],
  "count": 1
}
```

#### 2. get_cluster

Get details of a specific cluster by name.

**Parameters:**
- `name` (required): Name of the cluster to retrieve

**Example:**

```json
{
  "name": "get_cluster",
  "arguments": {
    "name": "cluster1"
  }
}
```

**Response:**
```json
{
  "name": "cluster1",
  "labels": {
    "region": "us-west",
    "environment": "production"
  },
  "clusterClaims": {
    "id.k8s.io": "cluster1-id",
    "kubeversion.open-cluster-management.io": "v1.24.0"
  },
  "status": "Available",
  "accepted": true,
  "capacity": {
    "cpu": "16",
    "memory": "64Gi"
  },
  "allocatable": {
    "cpu": "15",
    "memory": "60Gi"
  },
  "kubeVersion": "v1.24.0"
}
```

## Common Use Cases

### Filter by Environment

List all production clusters:
```json
{
  "name": "list_clusters",
  "arguments": {
    "labels": {
      "environment": "production"
    }
  }
}
```

### Filter by Region

List all clusters in a specific region:
```json
{
  "name": "list_clusters",
  "arguments": {
    "labels": {
      "region": "us-west"
    }
  }
}
```

### Multiple Label Filters

List production clusters in a specific region:
```json
{
  "name": "list_clusters",
  "arguments": {
    "labelSelector": "environment=production,region=us-west"
  }
}
```

### List All Clusters

List all clusters without filtering:
```json
{
  "name": "list_clusters",
  "arguments": {}
}
```

## MCP Protocol

The server implements the Model Context Protocol (MCP) specification:

- **Method: initialize** - Initialize the server and return server info
- **Method: tools/list** - List available tools
- **Method: tools/call** - Execute a tool

All communication is done via JSON-RPC over stdin/stdout.

## Cluster Information Fields

- **name**: Cluster name
- **labels**: Map of all cluster labels
- **clusterClaims**: Custom cluster information (version, ID, etc.)
- **status**: Cluster health status (Available/Unavailable/Unknown)
- **accepted**: Whether the hub has accepted the cluster
- **capacity**: Total cluster resources (CPU, memory)
- **allocatable**: Available cluster resources
- **kubeVersion**: Kubernetes version (extracted from cluster claims)

## Requirements

- Go 1.25.0 or higher
- Access to an OCM hub cluster
- Valid kubeconfig with permissions to list ManagedCluster resources

## Development

```bash
# Build from mcp directory
go build -o ocm-cluster-mcp

# Or build from repository root
go build -o mcp/ocm-cluster-mcp ./mcp

# Run with debug logging
./ocm-cluster-mcp -v=4
```

## License

Apache License 2.0
