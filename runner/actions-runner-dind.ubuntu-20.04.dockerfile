FROM ubuntu:20.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION
ARG RUNNER_CONTAINER_HOOKS_VERSION
# Docker and Docker Compose arguments
ARG CHANNEL=stable
ARG DOCKER_VERSION=24.0.7
ARG DOCKER_COMPOSE_VERSION=v2.23.0
ARG DUMB_INIT_VERSION=1.2.5

# Use 1001 and 121 for compatibility with GitHub-hosted runners
ARG RUNNER_UID=1000
ARG DOCKER_GID=1001

ENV DEBIAN_FRONTEND=noninteractive
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
RUN adduser --disabled-password --gecos "" --uid $RUNNER_UID runner \
    && groupadd docker --gid $DOCKER_GID \
    && usermod -aG sudo runner \
    && usermod -aG docker runner \
    && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers \
    && echo "Defaults env_keep += \"DEBIAN_FRONTEND\"" >> /etc/sudoers

ENV HOME=/home/runner

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
    && rm -f runner.tar.gz \
    && ./bin/installdependencies.sh \
    # libyaml-dev is required for ruby/setup-ruby action.
    # It is installed after installdependencies.sh and before removing /var/lib/apt/lists
    # to avoid rerunning apt-update on its own.
    && apt-get install -y libyaml-dev \
    && rm -rf /var/lib/apt/lists/*

ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache
RUN mkdir /opt/hostedtoolcache \
    && chgrp docker /opt/hostedtoolcache \
    && chmod g+rwx /opt/hostedtoolcache

RUN cd "$RUNNER_ASSETS_DIR" \
    && curl -fLo runner-container-hooks.zip https://github.com/actions/runner-container-hooks/releases/download/v${RUNNER_CONTAINER_HOOKS_VERSION}/actions-runner-hooks-k8s-${RUNNER_CONTAINER_HOOKS_VERSION}.zip \
    && unzip ./runner-container-hooks.zip -d ./k8s \
    && rm -f runner-container-hooks.zip

RUN set -vx; \
    export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && curl -fLo docker.tgz https://download.docker.com/linux/static/${CHANNEL}/${ARCH}/docker-${DOCKER_VERSION}.tgz \
    && tar zxvf docker.tgz \
    && install -o root -g root -m 755 docker/* /usr/bin/ \
    && rm -rf docker docker.tgz

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && mkdir -p /usr/libexec/docker/cli-plugins \
    && curl -fLo /usr/libexec/docker/cli-plugins/docker-compose https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${ARCH} \
    && chmod +x /usr/libexec/docker/cli-plugins/docker-compose \
    && ln -s /usr/libexec/docker/cli-plugins/docker-compose /usr/bin/docker-compose \
    && which docker-compose \
    && docker compose version

# We place the scripts in `/usr/bin` so that users who extend this image can
# override them with scripts of the same name placed in `/usr/local/bin`.
COPY entrypoint-dind.sh startup.sh logger.sh wait.sh graceful-stop.sh update-status /usr/bin/
RUN chmod +x /usr/bin/entrypoint-dind.sh /usr/bin/startup.sh

# Copy the docker shim which propagates the docker MTU to underlying networks
# to replace the docker binary in the PATH.
COPY docker-shim.sh /usr/local/bin/docker

# Configure hooks folder structure.
COPY hooks /etc/arc/hooks/

VOLUME /var/lib/docker

# Add the Python "User Script Directory" to the PATH
ENV PATH="${PATH}:${HOME}/.local/bin"
ENV ImageOS=ubuntu20

RUN echo "PATH=${PATH}" > /etc/environment \
    && echo "ImageOS=${ImageOS}" >> /etc/environment

# No group definition, as that makes it harder to run docker.
USER runner

ENTRYPOINT ["/bin/bash", "-c"]
CMD ["entrypoint-dind.sh"]
