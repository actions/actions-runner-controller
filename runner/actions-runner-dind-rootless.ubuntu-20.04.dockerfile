FROM ubuntu:20.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION
ARG RUNNER_CONTAINER_HOOKS_VERSION
# Docker and Docker Compose arguments
ENV CHANNEL=stable
ARG DOCKER_COMPOSE_VERSION=v2.23.0
ARG DUMB_INIT_VERSION=1.2.5

# Other arguments
ARG DEBUG=false

ENV DEBIAN_FRONTEND=noninteractive

# Use 1001 for compatibility with GitHub-hosted runners
ARG RUNNER_UID=1000

RUN apt-get update -y \
    && apt-get install -y software-properties-common \
    && add-apt-repository -y ppa:git-core/ppa \
    && apt-get update -y \
    && apt-get install -y --no-install-recommends \
    build-essential \
    curl \
    ca-certificates \
    dnsutils \
    ftp \
    git \
    iproute2 \
    iputils-ping \
    iptables \
    jq \
    libunwind8 \
    locales \
    netcat \
    net-tools \
    openssh-client \
    parallel \
    python3-pip \
    rsync \
    shellcheck \
    software-properties-common \
    sudo \
    telnet \
    time \
    tzdata \
    uidmap \
    unzip \
    upx \
    wget \
    zip \
    zstd \
    && ln -sf /usr/bin/python3 /usr/bin/python \
    && ln -sf /usr/bin/pip3 /usr/bin/pip \
    && rm -rf /var/lib/apt/lists/*

# Download latest git-lfs version
RUN curl -s https://packagecloud.io/install/repositories/github/git-lfs/script.deb.sh | bash && \
    apt-get install -y --no-install-recommends git-lfs

# Runner user
RUN adduser --disabled-password --gecos "" --uid $RUNNER_UID runner

ENV HOME=/home/runner

# Set-up subuid and subgid so that "--userns-remap=default" works
RUN set -eux; \
    addgroup --system dockremap; \
    adduser --system --ingroup dockremap dockremap; \
    echo 'dockremap:165536:65536' >> /etc/subuid; \
    echo 'dockremap:165536:65536' >> /etc/subgid

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && curl -fLo /usr/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v${DUMB_INIT_VERSION}/dumb-init_${DUMB_INIT_VERSION}_${ARCH} \
    && chmod +x /usr/bin/dumb-init

# Set this to /home/runner for compatibility with v1 runners
ARG RUNNER_ASSETS_DIR=/runnertmp
ENV RUNNER_ASSETS_DIR=$RUNNER_ASSETS_DIR
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x64 ; fi \
    && mkdir -p "$RUNNER_ASSETS_DIR" \
    && cd "$RUNNER_ASSETS_DIR" \
    && curl -fLo runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz \
    && tar xzf ./runner.tar.gz \
    && rm runner.tar.gz \
    && ./bin/installdependencies.sh \
    && mv ./externals ./externalstmp \
    # libyaml-dev is required for ruby/setup-ruby action.
    # It is installed after installdependencies.sh and before removing /var/lib/apt/lists
    # to avoid rerunning apt-update on its own.
    && apt-get install -y libyaml-dev \
    && rm -rf /var/lib/apt/lists/*

ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache
RUN mkdir /opt/hostedtoolcache \
    && chgrp runner /opt/hostedtoolcache \
    && chmod g+rwx /opt/hostedtoolcache

RUN cd "$RUNNER_ASSETS_DIR" \
    && curl -fLo runner-container-hooks.zip https://github.com/actions/runner-container-hooks/releases/download/v${RUNNER_CONTAINER_HOOKS_VERSION}/actions-runner-hooks-k8s-${RUNNER_CONTAINER_HOOKS_VERSION}.zip \
    && unzip ./runner-container-hooks.zip -d ./k8s \
    && rm -f runner-container-hooks.zip

# Make the rootless runner directory executable
RUN mkdir /run/user/$RUNNER_UID \
    && chown runner:runner /run/user/$RUNNER_UID \
    && chmod a+x /run/user/$RUNNER_UID

# We place the scripts in `/usr/bin` so that users who extend this image can
# override them with scripts of the same name placed in `/usr/local/bin`.
COPY entrypoint-dind-rootless.sh startup.sh logger.sh graceful-stop.sh update-status /usr/bin/
RUN chmod +x /usr/bin/entrypoint-dind-rootless.sh /usr/bin/startup.sh

# Copy the docker shim which propagates the docker MTU to underlying networks
# to replace the docker binary in the PATH.
COPY docker-shim.sh /usr/local/bin/docker

# Configure hooks folder structure.
COPY hooks /etc/arc/hooks/

# Add the Python "User Script Directory" to the PATH
ENV PATH="${PATH}:${HOME}/.local/bin:/home/runner/bin"
ENV ImageOS=ubuntu20
ENV DOCKER_HOST=unix:///run/user/$RUNNER_UID/docker.sock
ENV XDG_RUNTIME_DIR=/run/user/$RUNNER_UID

RUN echo "PATH=${PATH}" > /etc/environment \
    && echo "ImageOS=${ImageOS}" >> /etc/environment \
    && echo "DOCKER_HOST=${DOCKER_HOST}" >> /etc/environment \
    && echo "XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR}" >> /etc/environment

# No group definition, as that makes it harder to run docker.
USER runner

# This will install docker under $HOME/bin according to the content of the script
RUN export SKIP_IPTABLES=1 \
    && curl -fsSL https://get.docker.com/rootless | sh \
    && /home/runner/bin/docker -v

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && mkdir -p /home/runner/.docker/cli-plugins \
    && curl -fLo /home/runner/.docker/cli-plugins/docker-compose https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${ARCH} \
    && chmod +x /home/runner/.docker/cli-plugins/docker-compose \
    && ln -s /home/runner/.docker/cli-plugins/docker-compose /home/runner/bin/docker-compose \
    && which docker-compose \
    && docker compose version

# Create folder structure here to avoid permission issues
# when mounting the daemon.json file from a configmap.
RUN mkdir -p /home/runner/.config/docker

ENTRYPOINT ["/bin/bash", "-c"]
CMD ["entrypoint-dind-rootless.sh"]
