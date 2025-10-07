FROM ubuntu:24.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION
ARG RUNNER_CONTAINER_HOOKS_VERSION
ARG CHANNEL=stable
ARG DOCKER_VERSION=24.0.7
ARG DOCKER_COMPOSE_VERSION=v2.23.0
ARG DUMB_INIT_VERSION=1.2.5
ARG RUNNER_USER_UID=1001
ARG DOCKER_GROUP_GID=121

ENV DEBIAN_FRONTEND=noninteractive

# Core dependencies + Git
RUN apt-get update -y \
 && apt-get install -y software-properties-common ca-certificates curl wget gnupg \
 && add-apt-repository -y ppa:git-core/ppa \
 && apt-get update -y \
 && apt-get install -y --no-install-recommends \
      bash sudo git jq unzip zip tar xz-utils \
      iproute2 dnsutils tzdata \
 && rm -rf /var/lib/apt/lists/*

# Locale support: en_US.UTF-8
RUN apt-get update -y && apt-get install -y --no-install-recommends locales \
 && locale-gen en_US.UTF-8 \
 && update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8 \
 && echo 'LANG=en_US.UTF-8' >> /etc/environment \
 && echo 'LC_ALL=en_US.UTF-8' >> /etc/environment \
 && echo 'LANGUAGE=en_US.UTF-8' >> /etc/environment \
 && rm -rf /var/lib/apt/lists/*

ENV LANG=en_US.UTF-8 \
    LANGUAGE=en_US.UTF-8 \
    LC_ALL=en_US.UTF-8

# git-lfs
RUN curl -s https://packagecloud.io/install/repositories/github/git-lfs/script.deb.sh | bash \
 && apt-get update -y && apt-get install -y --no-install-recommends git-lfs \
 && rm -rf /var/lib/apt/lists/*

# runner user & docker group
RUN adduser --disabled-password --gecos "" --uid $RUNNER_USER_UID runner \
 && groupadd docker --gid $DOCKER_GROUP_GID \
 && usermod -aG sudo runner \
 && usermod -aG docker runner \
 && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers \
 && echo "Defaults env_keep += \"DEBIAN_FRONTEND\"" >> /etc/sudoers

ENV HOME=/home/runner

# dumb-init as PID1
RUN ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
 && [ "$ARCH" = "arm64" ] && ARCH=aarch64 || true \
 && [ "$ARCH" = "amd64" ] && ARCH=x86_64 || true \
 && curl -fsSL -o /usr/bin/dumb-init \
      https://github.com/Yelp/dumb-init/releases/download/v${DUMB_INIT_VERSION}/dumb-init_${DUMB_INIT_VERSION}_${ARCH} \
 && chmod +x /usr/bin/dumb-init

ENV RUNNER_ASSETS_DIR=/runnertmp
RUN ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
 && [ "$ARCH" = "amd64" ] || [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "i386" ] && ARCH=x64 || true \
 && mkdir -p "$RUNNER_ASSETS_DIR" \
 && cd "$RUNNER_ASSETS_DIR" \
 && curl -fsSL -o runner.tar.gz "https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz" \
 && tar xzf runner.tar.gz && rm runner.tar.gz \
 && ./bin/installdependencies.sh \
 && mv ./externals ./externalstmp \
 && apt-get update -y && apt-get install -y --no-install-recommends libyaml-dev \
 && rm -rf /var/lib/apt/lists/*

# Toolcache dirs + envs (both)
ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache
ENV AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache
RUN mkdir -p /opt/hostedtoolcache \
 && chgrp docker /opt/hostedtoolcache \
 && chmod g+rwx /opt/hostedtoolcache

# Runner container hooks (K8s)
ENV RUNNER_CONTAINER_HOOKS=/runnertmp/k8s
RUN cd "$RUNNER_ASSETS_DIR" \
 && curl -fsSL -o runner-container-hooks.zip \
      "https://github.com/actions/runner-container-hooks/releases/download/v${RUNNER_CONTAINER_HOOKS_VERSION}/actions-runner-hooks-k8s-${RUNNER_CONTAINER_HOOKS_VERSION}.zip" \
 && unzip -q runner-container-hooks.zip -d ./k8s \
 && rm -f runner-container-hooks.zip

# Docker CLI + compose plugin (client only; DinD sidecar provides daemon)
RUN set -eux; \
    ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2); \
    [ "$ARCH" = "arm64" ] && DARCH=aarch64 || true; \
    [ "$ARCH" = "amd64" ] && DARCH=x86_64 || true; \
    curl -fsSL -o docker.tgz "https://download.docker.com/linux/static/${CHANNEL}/${DARCH}/docker-${DOCKER_VERSION}.tgz"; \
    tar -xzf docker.tgz; install -m755 docker/docker /usr/bin/docker; rm -rf docker docker.tgz; \
    mkdir -p /usr/libexec/docker/cli-plugins; \
    curl -fsSL -o /usr/libexec/docker/cli-plugins/docker-compose \
      "https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${DARCH}"; \
    chmod +x /usr/libexec/docker/cli-plugins/docker-compose; \
    ln -sf /usr/libexec/docker/cli-plugins/docker-compose /usr/bin/docker-compose; \
    docker compose version >/dev/null

# add 'make' and build tools
RUN apt-get update -y \
 && apt-get install -y software-properties-common ca-certificates curl wget gnupg \
 && add-apt-repository -y ppa:git-core/ppa \
 && apt-get update -y \
 && apt-get install -y --no-install-recommends \
      bash sudo git git-lfs jq unzip zip tar xz-utils \
      iproute2 dnsutils tzdata \
      make \
      build-essential pkg-config \
 && rm -rf /var/lib/apt/lists/*

# official GitHub CLI installation https://github.com/cli/cli/blob/trunk/docs/install_linux.md#debian
RUN (type -p wget >/dev/null || (apt update && apt install wget -y)) \
 && mkdir -p -m 755 /etc/apt/keyrings \
 && out=$(mktemp) && wget -nv -O$out https://cli.github.com/packages/githubcli-archive-keyring.gpg \
 && cat $out | tee /etc/apt/keyrings/githubcli-archive-keyring.gpg > /dev/null \
 && chmod go+r /etc/apt/keyrings/githubcli-archive-keyring.gpg \
 && mkdir -p -m 755 /etc/apt/sources.list.d \
 && echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" | tee /etc/apt/sources.list.d/github-cli.list > /dev/null \
 && apt update \
 && apt install gh -y \
 && rm -rf /var/lib/apt/lists/*


# Your scripts and hooks
COPY entrypoint.sh startup.sh logger.sh graceful-stop.sh update-status /usr/bin/
COPY docker-shim.sh /usr/local/bin/docker
COPY hooks /etc/arc/hooks/

# PATH & ImageOS
ENV PATH="${PATH}:${HOME}/.local/bin/"
ENV ImageOS=ubuntu24
RUN echo "PATH=${PATH}" > /etc/environment && echo "ImageOS=${ImageOS}" >> /etc/environment

USER runner

ENTRYPOINT ["/usr/bin/dumb-init","--"]
CMD ["entrypoint.sh"]
