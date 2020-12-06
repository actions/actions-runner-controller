FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive
# Dev + DinD dependencies
RUN apt update \
    && apt install -y software-properties-common \
    && add-apt-repository -y ppa:git-core/ppa \
    && apt install -y \
    build-essential \
    curl \
    ca-certificates \
    dnsutils \
    ftp \
    git \
    iproute2 \
    iptables \
    iputils-ping \
    jq \
    libunwind8 \
    locales \
    netcat \
    openssh-client \
    parallel \
    rsync \
    shellcheck \
    sudo \
    supervisor \
    telnet \
    time \
    tzdata \
    unzip \
    upx \
    wget \
    zip \
    zstd \
    && rm -rf /var/lib/apt/list/*

# Runner user
RUN adduser --disabled-password --gecos "" --uid 1000 runner \
    && groupadd docker \
    && usermod -aG sudo runner \
    && usermod -aG docker runner \
    && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers

ARG TARGETPLATFORM
ARG RUNNER_VERSION=2.274.1
ARG DOCKER_CHANNEL=stable
ARG DOCKER_VERSION=19.03.13
ARG DEBUG=false

# Docker installation
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
    && if [ "$ARCH" = "amd64" ]; then export ARCH=x86_64 ; fi \
	&& if ! curl -L -o docker.tgz "https://download.docker.com/linux/static/${DOCKER_CHANNEL}/${ARCH}/docker-${DOCKER_VERSION}.tgz"; then \
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

# Runner download supports amd64 as x64
#
# libyaml-dev is required for ruby/setup-ruby action.
# It is installed after installdependencies.sh and before removing /var/lib/apt/lists
# to avoid rerunning apt-update on its own.
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && if [ "$ARCH" = "amd64" ]; then export ARCH=x64 ; fi \
    && mkdir -p /runner \
     && cd /runner \
    && curl -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz \
    && tar xzf ./runner.tar.gz \
    && rm runner.tar.gz \
    && ./bin/installdependencies.sh \
    && apt-get install -y libyaml-dev \
    && rm -rf /var/lib/apt/lists/*

RUN echo AGENT_TOOLSDIRECTORY=/opt/hostedtoolcache > /runner.env \
  && mkdir /opt/hostedtoolcache \
  && chgrp runner /opt/hostedtoolcache \
  && chmod g+rwx /opt/hostedtoolcache

COPY modprobe startup.sh /usr/local/bin/
COPY supervisor/ /etc/supervisor/conf.d/
COPY logger.sh /opt/bash-utils/logger.sh
COPY entrypoint.sh /usr/local/bin/

RUN chmod +x /usr/local/bin/startup.sh /usr/local/bin/entrypoint.sh /usr/local/bin/modprobe

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
    && curl -L -o /usr/local/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v1.2.2/dumb-init_1.2.2_${ARCH} \
    && chmod +x /usr/local/bin/dumb-init

VOLUME /var/lib/docker

COPY patched /runner/patched

# No group definition, as that makes it harder to run docker.
USER runner

ENTRYPOINT ["/usr/local/bin/dumb-init", "--"]
CMD ["startup.sh"]
