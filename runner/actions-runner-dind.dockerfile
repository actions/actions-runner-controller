FROM ubuntu:20.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION=2.293.0
ARG DOCKER_CHANNEL=stable
ARG DOCKER_VERSION=20.10.12
ARG DUMB_INIT_VERSION=1.2.5

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
    unzip \
    upx \
    wget \
    zip \
    zstd \
    && ln -sf /usr/bin/python3 /usr/bin/python \
    && ln -sf /usr/bin/pip3 /usr/bin/pip \
    && rm -rf /var/lib/apt/lists/*

# Runner user
RUN adduser --disabled-password --gecos "" --uid 1000 runner \
    && groupadd docker \
    && usermod -aG sudo runner \
    && usermod -aG docker runner \
    && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers

# arch command on OS X reports "i386" for Intel CPUs regardless of bitness
# Docker download supports arm64 as aarch64 & amd64 / i386 as x86_64
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
	&& if ! curl -f -L -o docker.tgz "https://download.docker.com/linux/static/${DOCKER_CHANNEL}/${ARCH}/docker-${DOCKER_VERSION}.tgz"; then \
		echo >&2 "error: failed to download 'docker-${DOCKER_VERSION}' from '${DOCKER_CHANNEL}' for '${ARCH}'"; \
		exit 1; \
	fi; \
    echo "Downloaded Docker from https://download.docker.com/linux/static/${DOCKER_CHANNEL}/${ARCH}/docker-${DOCKER_VERSION}.tgz"; \
	tar --extract \
		--file docker.tgz \
		--strip-components 1 \
		--directory /usr/local/bin/ \
	; \
	rm docker.tgz; \
	dockerd --version; \
	docker --version

ENV HOME=/home/runner

# Runner download supports amd64 as x64
#
# libyaml-dev is required for ruby/setup-ruby action.
# It is installed after installdependencies.sh and before removing /var/lib/apt/lists
# to avoid rerunning apt-update on its own.
ENV RUNNER_ASSETS_DIR=/runnertmp
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "x86_64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x64 ; fi \
    && mkdir -p "$RUNNER_ASSETS_DIR" \
    && cd "$RUNNER_ASSETS_DIR" \
    && curl -f -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz \
    && tar xzf ./runner.tar.gz \
    && rm runner.tar.gz \
    && ./bin/installdependencies.sh \
    && apt-get install -y libyaml-dev \
    && rm -rf /var/lib/apt/lists/*

ENV RUNNER_TOOL_CACHE=/opt/hostedtoolcache
RUN mkdir /opt/hostedtoolcache \
    && chgrp docker /opt/hostedtoolcache \
    && chmod g+rwx /opt/hostedtoolcache

# We place the scripts in `/usr/bin` so that users who extend this image can
# override them with scripts of the same name placed in `/usr/local/bin`.
COPY entrypoint.sh logger.bash startup.sh /usr/bin/
COPY supervisor/ /etc/supervisor/conf.d/
RUN chmod +x /usr/bin/startup.sh /usr/bin/entrypoint.sh

# arch command on OS X reports "i386" for Intel CPUs regardless of bitness
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ] || [ "$ARCH" = "i386" ]; then export ARCH=x86_64 ; fi \
    && curl -f -L -o /usr/local/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v${DUMB_INIT_VERSION}/dumb-init_${DUMB_INIT_VERSION}_${ARCH} \
    && chmod +x /usr/local/bin/dumb-init

VOLUME /var/lib/docker

# Add the Python "User Script Directory" to the PATH
ENV PATH="${PATH}:${HOME}/.local/bin"
ENV ImageOS=ubuntu20

RUN echo "PATH=${PATH}" > /etc/environment \
    && echo "ImageOS=${ImageOS}" >> /etc/environment

# No group definition, as that makes it harder to run docker.
USER runner

ENTRYPOINT ["/usr/local/bin/dumb-init", "--"]
CMD ["startup.sh"]
