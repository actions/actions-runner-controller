# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.18.3 as builder

WORKDIR /workspace

# Make it runnable on a distroless image/without libc
ENV CGO_ENABLED=0

# Copy the Go Modules manifests
COPY go.mod go.sum ./

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer.
#
# Also, we need to do this before setting TARGETPLATFORM/TARGETOS/TARGETARCH/TARGETVARIANT
# so that go mod cache is shared across platforms.
RUN go mod download

# Copy the go source
# COPY . .

# Usage:
#   docker buildx build --tag repo/img:tag -f ./Dockerfile . --platform linux/amd64,linux/arm64,linux/arm/v7
#
# With the above commmand,
# TARGETOS can be "linux", TARGETARCH can be "amd64", "arm64", and "arm", TARGETVARIANT can be "v7".

ARG TARGETPLATFORM TARGETOS TARGETARCH TARGETVARIANT

# We intentionally avoid `--mount=type=cache,mode=0777,target=/go/pkg/mod` in the `go mod download` and the `go build` runs
# to avoid https://github.com/moby/buildkit/issues/2334
# We can use docker layer cache so the build is fast enogh anyway
# We also use per-platform GOCACHE for the same reason.
env GOCACHE /build/${TARGETPLATFORM}/root/.cache/go-build

# Build
RUN --mount=target=. \
  --mount=type=cache,mode=0777,target=${GOCACHE} \
  export GOOS=${TARGETOS} GOARCH=${TARGETARCH} GOARM=${TARGETVARIANT#v} && \
  go build -o /out/manager main.go && \
  go build -o /out/github-webhook-server ./cmd/githubwebhookserver

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /out/manager .
COPY --from=builder /out/github-webhook-server .

USER nonroot:nonroot

ENTRYPOINT ["/manager"]
