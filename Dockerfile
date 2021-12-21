# Build the manager binary
FROM pratikimprowise/upx:3.96 as upx
FROM golang:1.17 as builder
COPY --from=upx / /
WORKDIR /workspace

ENV GO111MODULE=on

# Copy the Go Modules manifests
COPY go.mod go.sum ./

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY . .

ARG TARGETARCH TARGETOS

# Build
ENV CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH"
RUN export GOARM=$(echo ${TARGETPLATFORM} | cut -d / -f3 | cut -c2-) && \
  go build -a -o manager main.go && \
  go build -a -o github-webhook-server ./cmd/githubwebhookserver

## Compress binary with upx https://github.com/upx/upx/
RUN upx -9 /workspace/manager || true && \
  upx -9 /workspace/github-webhook-server || true

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /workspace/manager .
COPY --from=builder /workspace/github-webhook-server .

USER nonroot:nonroot

ENTRYPOINT ["/manager"]
