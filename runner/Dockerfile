FROM ubuntu:18.04

ARG TARGETPLATFORM
ARG RUNNER_VERSION=2.274.2
ARG DOCKER_VERSION=19.03.12

ENV DEBIAN_FRONTEND=noninteractive
RUN apt update -y \
  && apt install -y software-properties-common \
  && add-apt-repository -y ppa:git-core/ppa \
  && apt update -y \
  && apt install -y --no-install-recommends \
  build-essential \
  curl \
  ca-certificates \
  dnsutils \
  ftp \
  git \
  iproute2 \
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
  telnet \
  time \
  tzdata \
  unzip \
  upx \
  wget \
  zip \
  zstd \
  && rm -rf /var/lib/apt/lists/*

RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
  && curl -L -o /usr/local/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v1.2.2/dumb-init_1.2.2_${ARCH} \
  && chmod +x /usr/local/bin/dumb-init

# Docker download supports arm64 as aarch64 & amd64 as x86_64
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
  && if [ "$ARCH" = "arm64" ]; then export ARCH=aarch64 ; fi \
  && if [ "$ARCH" = "amd64" ]; then export ARCH=x86_64 ; fi \
  && curl -L -o docker.tgz https://download.docker.com/linux/static/stable/${ARCH}/docker-${DOCKER_VERSION}.tgz \
  && tar zxvf docker.tgz \
  && install -o root -g root -m 755 docker/docker /usr/local/bin/docker \
  && rm -rf docker docker.tgz \
  && adduser --disabled-password --gecos "" --uid 1000 runner \
  && groupadd docker \
  && usermod -aG sudo runner \
  && usermod -aG docker runner \
  && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers

# Runner download supports amd64 as x64. Externalstmp is needed for making mount points work inside DinD.
RUN export ARCH=$(echo ${TARGETPLATFORM} | cut -d / -f2) \
  && if [ "$ARCH" = "amd64" ]; then export ARCH=x64 ; fi \
  && mkdir -p /runner \
  && cd /runner \
  && curl -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-${ARCH}-${RUNNER_VERSION}.tar.gz \
  && tar xzf ./runner.tar.gz \
  && rm runner.tar.gz \
  && ./bin/installdependencies.sh \
  && mv ./externals ./externalstmp \
  && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /runner
COPY patched /runner/patched

USER runner
ENTRYPOINT ["/usr/local/bin/dumb-init", "--"]
CMD ["/runner/entrypoint.sh"]
