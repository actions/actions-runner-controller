FROM --platform=$BUILDPLATFORM tonistiigi/xx AS xx
FROM --platform=$BUILDPLATFORM golang:1.17-alpine AS base
COPY --from=xx / /
ENV CGO_ENABLED=0
ENV GO111MODULE=on
WORKDIR /src

FROM base AS vendored
COPY go.* .
RUN go mod tidy && go mod download

FROM vendored AS build
ARG TARGETPLATFORM
RUN xx-apk add gcc musl-dev ca-certificates
COPY . .
RUN xx-go build -a -o /app/manager main.go && \
  xx-go build -a -o /app/github-webhook-server ./cmd/githubwebhookserver

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=build /app/ /
USER nonroot:nonroot
ENTRYPOINT ["/manager"]
