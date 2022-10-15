FROM ubuntu:20.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION=2.298.2
# Docker and Docker Compose arguments
ENV CHANNEL=stable
ARG DOCKER_COMPOSE_VERSION=v2.6.0
ARG DUMB_INIT_VERSION=1.2.5

# Other arguments
ARG DEBUG=false

RUN test -n "$TARGETPLATFORM" || (echo "TARGETPLATFORM must be set" && false)

ENV DEBIAN_FRONTEND=noninteractive
RUN apt update -y \
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
    git-lfs \
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
    supervisor \
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

# Runner user
RUN adduser --disabled-password --gecos "" --uid 1000 runner

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
    && curl -f -L -o /usr/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v${DUMB_INIT_VERSION}/dumb-init_${DUMB_INIT_VERSION}_${ARCH} \
    && chmod +x /usr/bin/dumb-init

ENV RUNNER_ASSETS_DIR=/runnertmp
RUN ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && export ARCH \
    && if [ "$ARCH" = "amd64" ]; then export ARCH=x64 ; fi \
    && mkdir -p "$RUNNER_ASSETS_DIR" \
    && cd "$RUNNER_ASSETS_DIR" \
    && curl -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz \
    && tar xzf ./runner.tar.gz \
    && rm runner.tar.gz \
    && ./bin/installdependencies.sh \
    # libyaml-dev is required for ruby/setup-ruby action.
    # It is installed after installdependencies.sh and before removing /var/lib/apt/lists
    # to avoid rerunning apt-update on its own.
    && apt-get install -y libyaml-dev \
    && rm -rf /var/lib/apt/lists/*

RUN echo AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache > /runner.env \
    && mkdir /opt/hostedtoolcache \
    && chgrp runner /opt/hostedtoolcache \
    && chmod g+rwx /opt/hostedtoolcache

# This will install docker under $HOME/bin according to the content of the script
ENV SKIP_IPTABLES=1
RUN curl -fsSL https://get.docker.com/rootless | sh

# Make the rootless runner directory executable
RUN mkdir /run/user/1000 \
    && chown runner:runner /run/user/1000 \
    && chmod a+x /run/user/1000

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && curl -fLo /usr/bin/docker-compose https://github.com/docker/compose/releases/download/${DOCKER_COMPOSE_VERSION}/docker-compose-linux-${ARCH} \
    && chmod +x /usr/bin/docker-compose

# We place the scripts in `/usr/bin` so that users who extend this image can
# override them with scripts of the same name placed in `/usr/local/bin`.
COPY entrypoint.sh logger.bash rootless-startup.sh update-status /usr/bin/
RUN chmod +x /usr/bin/rootless-startup.sh /usr/bin/entrypoint.sh

# Copy the docker shim which propagates the docker MTU to underlying networks
# to replace the docker binary in the PATH.
COPY docker-shim.sh /usr/local/bin/docker

# Configure hooks folder structure.
COPY hooks /etc/arc/hooks/

# Add the Python "User Script Directory" to the PATH
ENV PATH="${PATH}:${HOME}/.local/bin:/home/runner/bin"
ENV ImageOS=ubuntu20
ENV DOCKER_HOST=unix:///run/user/1000/docker.sock
ENV XDG_RUNTIME_DIR=/run/user/1000

RUN echo "PATH=${PATH}" > /etc/environment \
    && echo "ImageOS=${ImageOS}" >> /etc/environment \
    && echo "DOCKER_HOST=${DOCKER_HOST}" >> /etc/environment \
    && echo "XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR}" >> /etc/environment

# No group definition, as that makes it harder to run docker.
USER runner

ENTRYPOINT ["/usr/bin/dumb-init", "--"]
CMD ["rootless-startup.sh"]
