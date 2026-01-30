#!/bin/bash

# Deployment script for OCM Cluster MCP Server
set -e

# Configuration
REGISTRY=${REGISTRY:-"quay.io"}
USERNAME=${USERNAME:-"YOUR_USERNAME"}
IMAGE_NAME="ocm-cluster-mcp"
TAG=${TAG:-"latest"}
NAMESPACE="open-cluster-management"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

function print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

function print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

function print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

function check_prerequisites() {
    print_info "Checking prerequisites..."

    # Check if kubectl is installed
    if ! command -v kubectl &> /dev/null; then
        print_error "kubectl is not installed"
        exit 1
    fi

    # Check if docker or podman is installed
    if command -v docker &> /dev/null; then
        CONTAINER_CLI="docker"
    elif command -v podman &> /dev/null; then
        CONTAINER_CLI="podman"
    else
        print_error "Neither docker nor podman is installed"
        exit 1
    fi

    print_info "Using ${CONTAINER_CLI} as container CLI"

    # Check cluster connectivity
    if ! kubectl cluster-info &> /dev/null; then
        print_error "Cannot connect to Kubernetes cluster"
        exit 1
    fi

    print_info "Prerequisites check passed"
}

function build_image() {
    print_info "Building container image..."

    cd ..
    ${CONTAINER_CLI} build -f mcp/Dockerfile -t ${IMAGE_NAME}:${TAG} .
    cd mcp

    print_info "Image built successfully: ${IMAGE_NAME}:${TAG}"
}

function push_image() {
    FULL_IMAGE="${REGISTRY}/${USERNAME}/${IMAGE_NAME}:${TAG}"

    print_info "Tagging image as ${FULL_IMAGE}..."
    ${CONTAINER_CLI} tag ${IMAGE_NAME}:${TAG} ${FULL_IMAGE}

    print_info "Pushing image to registry..."
    ${CONTAINER_CLI} push ${FULL_IMAGE}

    print_info "Image pushed successfully: ${FULL_IMAGE}"
}

function update_manifests() {
    print_info "Updating deployment manifest with image: ${REGISTRY}/${USERNAME}/${IMAGE_NAME}:${TAG}"

    # Create a temporary file with updated image
    sed "s|quay.io/YOUR_QUAY_USERNAME/ocm-cluster-mcp:latest|${REGISTRY}/${USERNAME}/${IMAGE_NAME}:${TAG}|g" \
        deploy/deployment.yaml > deploy/deployment.yaml.tmp
    mv deploy/deployment.yaml.tmp deploy/deployment.yaml
}

function deploy() {
    print_info "Deploying to Kubernetes..."

    # Create namespace if it doesn't exist
    kubectl create namespace ${NAMESPACE} --dry-run=client -o yaml | kubectl apply -f -

    # Apply RBAC
    print_info "Applying RBAC resources..."
    kubectl apply -f deploy/rbac.yaml

    # Apply deployment
    print_info "Applying deployment..."
    kubectl apply -f deploy/deployment.yaml

    print_info "Deployment completed"
}

function wait_for_ready() {
    print_info "Waiting for pod to be ready..."
    kubectl wait --for=condition=ready pod -l app=ocm-cluster-mcp -n ${NAMESPACE} --timeout=120s
    print_info "Pod is ready"
}

function test_deployment() {
    print_info "Testing deployment..."

    POD_NAME=$(kubectl get pod -n ${NAMESPACE} -l app=ocm-cluster-mcp -o jsonpath='{.items[0].metadata.name}')

    if [ -z "$POD_NAME" ]; then
        print_error "No pod found"
        exit 1
    fi

    print_info "Testing with pod: ${POD_NAME}"

    # Test initialize
    print_info "Test 1: Initialize"
    echo '{"method":"initialize","params":{}}' | kubectl exec -i -n ${NAMESPACE} ${POD_NAME} -- cat

    # Test list tools
    print_info "Test 2: List tools"
    echo '{"method":"tools/list","params":{}}' | kubectl exec -i -n ${NAMESPACE} ${POD_NAME} -- cat

    # Test list clusters
    print_info "Test 3: List all clusters"
    echo '{"method":"tools/call","params":{"name":"list_clusters","arguments":{}}}' | kubectl exec -i -n ${NAMESPACE} ${POD_NAME} -- cat

    print_info "All tests completed"
}

function show_status() {
    print_info "Deployment status:"
    kubectl get pods,svc,deployment -n ${NAMESPACE} -l app=ocm-cluster-mcp

    print_info "Logs:"
    kubectl logs -n ${NAMESPACE} -l app=ocm-cluster-mcp --tail=20
}

function cleanup() {
    print_info "Cleaning up deployment..."
    kubectl delete -f deploy/deployment.yaml
    kubectl delete -f deploy/rbac.yaml
    print_info "Cleanup completed"
}

# Main script
case "${1}" in
    build)
        check_prerequisites
        build_image
        ;;
    push)
        check_prerequisites
        push_image
        ;;
    deploy)
        check_prerequisites
        update_manifests
        deploy
        wait_for_ready
        show_status
        ;;
    test)
        check_prerequisites
        test_deployment
        ;;
    status)
        show_status
        ;;
    cleanup)
        cleanup
        ;;
    all)
        check_prerequisites
        build_image
        push_image
        update_manifests
        deploy
        wait_for_ready
        test_deployment
        show_status
        ;;
    *)
        echo "Usage: $0 {build|push|deploy|test|status|cleanup|all}"
        echo ""
        echo "Commands:"
        echo "  build    - Build the container image"
        echo "  push     - Push the image to registry"
        echo "  deploy   - Deploy to Kubernetes"
        echo "  test     - Test the deployment"
        echo "  status   - Show deployment status"
        echo "  cleanup  - Remove the deployment"
        echo "  all      - Run all steps (build, push, deploy, test)"
        echo ""
        echo "Environment variables:"
        echo "  REGISTRY  - Container registry (default: quay.io)"
        echo "  USERNAME  - Registry username (default: YOUR_USERNAME)"
        echo "  TAG       - Image tag (default: latest)"
        echo ""
        echo "Example:"
        echo "  REGISTRY=quay.io USERNAME=myuser ./deploy.sh all"
        exit 1
        ;;
esac
