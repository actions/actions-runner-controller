# Build the manager binary
FROM golang:1.17 as builder

ARG TARGETPLATFORM

WORKDIR /workspace

ENV GO111MODULE=on \
  CGO_ENABLED=0

# # Copy the Go Modules manifests
# COPY go.mod go.sum ./

# # cache deps before building and copying source so that we don't need to re-download as much
# # and so that source changes don't invalidate our downloaded layer
# RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy the go source
# COPY . .

ARG TARGETOS
ARG TARGETARCH

# Build
RUN --mount=target=. \
  --mount=type=cache,mode=0777,target=/root/.cache/go-build \
  --mount=type=cache,mode=0777,target=/go/pkg/mod\
  GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
  GOARM=$(echo ${TARGETPLATFORM} | cut -d / -f3 | cut -c2-) \
  go build -o /out/manager main.go && go build -o /out/github-webhook-server ./cmd/githubwebhookserver

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/manager .
COPY --from=builder /out/github-webhook-server .

USER nonroot:nonroot

ENTRYPOINT ["/manager"]
