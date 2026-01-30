FROM golang:1.25-bookworm AS builder
ARG OS=linux
ARG ARCH=amd64
WORKDIR /go/src/open-cluster-management.io/ocm
COPY . .
ENV GO_PACKAGE open-cluster-management.io/ocm

RUN CGO_ENABLED=0 GOOS=${OS} GOARCH=${ARCH} \
    go build -a -o ocm-cluster-mcp ./mcp

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest
ENV USER_UID=10001

COPY --from=builder /go/src/open-cluster-management.io/ocm/ocm-cluster-mcp /

USER ${USER_UID}

ENTRYPOINT ["/ocm-cluster-mcp"]
