variable "GO_VERSION" {
  default = "1.17"
}

variable "REPO" {
  default = "summerwind/actions-runner-controller"
}

variable "VERSION" {
  default = "edge"
}

variable "GIT_SHA" {
  default = "0000000"
}

variable "TAGS" {
  default = [
    "${REPO}:latest",
    "${REPO}:${VERSION}",
    "${REPO}:${VERSION}-${GIT_SHA}"
  ]
}

variable "TAGS_SLIM" {
  default = [
    "${REPO}:latest-slim",
    "${REPO}:${VERSION}-slim",
    "${REPO}:${VERSION}-${GIT_SHA}-slim"
  ]
}

target "_common" {
  args = {
    GO_VERSION = GO_VERSION
  }
}

target "_slim" {
  target = "slim"
  tags   = TAGS
}

target "_fat" {
  target = "fat"
  tags   = fat
}

target "_labels" {
  labels = {
    "org.opencontainers.image.title"         = "actions-runner-controller",
    "org.opencontainers.image.base.name "    = "gcr.io/distroless/static:nonroot",
    "org.opencontainers.image.authors"       = "summerwind,mumoshu",
    "org.opencontainers.image.licenses"      = "Apache-2.0",
    "org.opencontainers.image.description"   = "Kubernetes controller for GitHub Actions self-hosted runners ",
    "org.opencontainers.image.version"       = "${VERSION}",
    "org.opencontainers.image.revision"      = "${GIT_SHA}",
    "org.opencontainers.image.source"        = "https://github.com/actions-runner-controller/actions-runner-controller",
    "org.opencontainers.image.documentation" = "https://github.com/actions-runner-controller/actions-runner-controller",
  }
}

target "_platform" {
  platforms = [
    "linux/amd64",
    "linux/arm64",
  ]
}

group "default" {
  targets = ["image-local"]
}

target "image-local" {
  inherits = ["_common", "_fat", "_labels"]
  output   = ["type=docker"]
}

target "image-slim" {
  inherits = ["_common", "_slim", "_labels"]
  output   = ["type=docker"]
}

target "image-all" {
  inherits = ["_common", "_fat", "_platform", "_labels"]
}

target "image-slim-all" {
  inherits = ["_common", "_slim", "_platform", "_labels"]
}

target "artifact" {
  inherits = ["_common"]
  target   = "artifact"
  output   = ["./dist"]
}

target "artifact-slim" {
  inherits = ["_common"]
  target   = "artifact-slim"
  output   = ["./dist"]
}

# Creating all fat, slim artifact with arm and amd platform
target "artifact-all" {
  inherits = ["_common", "_platform"]
  target   = "artifact-all"
  output   = ["./dist"]
}
