# Deploying OCM Cluster MCP Server to Kubernetes

This guide explains how to deploy the OCM Cluster MCP Server as a pod in your OCM hub cluster.

## Prerequisites

- Access to an OCM hub cluster with `kubectl` configured
- Docker or Podman installed for building the container image
- Access to a container registry (e.g., quay.io, docker.io)
- The `open-cluster-management` namespace exists in your cluster

## Step 1: Build the Container Image

From the repository root directory:

```bash
# Build the Docker image
docker build -f mcp/Dockerfile -t ocm-cluster-mcp:latest .

# Or using Podman
podman build -f mcp/Dockerfile -t ocm-cluster-mcp:latest .
```

## Step 2: Push to Container Registry

```bash
# Tag the image for your registry
docker tag ocm-cluster-mcp:latest quay.io/YOUR_USERNAME/ocm-cluster-mcp:latest

# Login to your registry
docker login quay.io

# Push the image
docker push quay.io/YOUR_USERNAME/ocm-cluster-mcp:latest
```

**Note:** Update `YOUR_USERNAME` with your actual registry username.

## Step 3: Update Deployment Manifest

Edit `deploy/deployment.yaml` and replace `YOUR_QUAY_USERNAME` with your actual username:

```yaml
image: quay.io/YOUR_USERNAME/ocm-cluster-mcp:latest
```

## Step 4: Deploy to Kubernetes

```bash
# Create the namespace if it doesn't exist
kubectl create namespace open-cluster-management --dry-run=client -o yaml | kubectl apply -f -

# Deploy RBAC resources
kubectl apply -f mcp/deploy/rbac.yaml

# Deploy the MCP server
kubectl apply -f mcp/deploy/deployment.yaml
```

## Step 5: Verify Deployment

```bash
# Check if the pod is running
kubectl get pods -n open-cluster-management -l app=ocm-cluster-mcp

# Check logs
kubectl logs -n open-cluster-management -l app=ocm-cluster-mcp

# Describe the deployment
kubectl describe deployment ocm-cluster-mcp -n open-cluster-management
```

## Step 6: Test the MCP Server

### Option 1: Test via kubectl exec

```bash
# Get the pod name
POD_NAME=$(kubectl get pod -n open-cluster-management -l app=ocm-cluster-mcp -o jsonpath='{.items[0].metadata.name}')

# Test initialize
echo '{"method":"initialize","params":{}}' | kubectl exec -i -n open-cluster-management $POD_NAME -- cat

# Test list tools
echo '{"method":"tools/list","params":{}}' | kubectl exec -i -n open-cluster-management $POD_NAME -- cat

# Test list clusters
echo '{"method":"tools/call","params":{"name":"list_clusters","arguments":{}}}' | kubectl exec -i -n open-cluster-management $POD_NAME -- cat
```

### Option 2: Test with port-forward

```bash
# Port forward to the pod (if you add HTTP endpoint later)
kubectl port-forward -n open-cluster-management svc/ocm-cluster-mcp 8080:8080
```

### Option 3: Interactive testing

```bash
# Connect to the pod interactively
kubectl exec -it -n open-cluster-management $POD_NAME -- sh

# Or attach to stdin/stdout
kubectl attach -it -n open-cluster-management $POD_NAME
```

## Testing MCP Tools

### List all clusters
```bash
echo '{"method":"tools/call","params":{"name":"list_clusters","arguments":{}}}' | \
  kubectl exec -i -n open-cluster-management $POD_NAME -- cat
```

### List clusters with label selector
```bash
echo '{"method":"tools/call","params":{"name":"list_clusters","arguments":{"labelSelector":"environment=production"}}}' | \
  kubectl exec -i -n open-cluster-management $POD_NAME -- cat
```

### Get a specific cluster
```bash
echo '{"method":"tools/call","params":{"name":"get_cluster","arguments":{"name":"cluster1"}}}' | \
  kubectl exec -i -n open-cluster-management $POD_NAME -- cat
```

## Troubleshooting

### Pod is not starting

Check the pod status and events:
```bash
kubectl describe pod -n open-cluster-management -l app=ocm-cluster-mcp
kubectl logs -n open-cluster-management -l app=ocm-cluster-mcp
```

### RBAC permissions issues

Verify the ServiceAccount has correct permissions:
```bash
kubectl auth can-i list managedclusters.cluster.open-cluster-management.io \
  --as=system:serviceaccount:open-cluster-management:ocm-cluster-mcp
```

### No managed clusters returned

Check if there are managed clusters in your hub:
```bash
kubectl get managedclusters
```

## Cleanup

To remove the MCP server deployment:

```bash
kubectl delete -f mcp/deploy/deployment.yaml
kubectl delete -f mcp/deploy/rbac.yaml
```

## Advanced Configuration

### Adjust resource limits

Edit `deploy/deployment.yaml` to adjust CPU and memory limits based on your cluster size.

### Enable debug logging

Add `-v=4` to the args in `deploy/deployment.yaml` for verbose logging.

### Multiple replicas

The MCP server is stateless and can run multiple replicas for high availability. Update `replicas` in the deployment.

## Integration with MCP Clients

To use this server with MCP clients (like Claude Desktop or other tools), you'll need to:

1. Expose the server via an Ingress or LoadBalancer
2. Configure the client to connect to the exposed endpoint
3. Or use `kubectl port-forward` for local development

Example client configuration:
```json
{
  "mcpServers": {
    "ocm-cluster": {
      "command": "kubectl",
      "args": [
        "exec",
        "-i",
        "-n",
        "open-cluster-management",
        "deployment/ocm-cluster-mcp",
        "--",
        "cat"
      ]
    }
  }
}
```
