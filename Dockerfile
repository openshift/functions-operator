# Build the manager binary
FROM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -o manager cmd/main.go

FROM builder AS builder-debug
# Build with debug symbols for delve
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -gcflags="all=-N -l" -o manager-debug cmd/main.go
# Install Delve
RUN go install github.com/go-delve/delve/cmd/dlv@latest


# Temporary until we have the latest features (registry-authfile, ...) in a released func cli version
FROM golang:1.26 AS func-cli-builder
ARG TARGETOS
ARG TARGETARCH

ARG FUNC_CLI_GH_REPO=knative/func
ARG FUNC_CLI_BRANCH=main

# workaround to invalidate cache when func cli repo got updated
ADD https://api.github.com/repos/${FUNC_CLI_GH_REPO}/git/refs/heads/${FUNC_CLI_BRANCH} version.json

WORKDIR /workspace
RUN git clone --branch ${FUNC_CLI_BRANCH} --single-branch --depth 1 https://github.com/${FUNC_CLI_GH_REPO} .
RUN make build

FROM registry.access.redhat.com/ubi9/ubi AS debug
WORKDIR /
COPY --from=builder-debug /workspace/manager-debug .
COPY --from=builder-debug /go/bin/dlv .
COPY --from=func-cli-builder /workspace/func /func/func
USER 65532:65532

ENTRYPOINT ["/dlv", "exec", "/manager-debug", "--headless", "--listen=:40000", "--api-version=2", "--accept-multiclient", "--log", "--"]

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot AS prod
WORKDIR /
COPY --from=builder /workspace/manager .
COPY --from=func-cli-builder /workspace/func /func/func
USER 65532:65532

ENTRYPOINT ["/manager"]
