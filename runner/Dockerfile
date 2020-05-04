FROM ubuntu:18.04

ARG RUNNER_VERSION
ARG DOCKER_VERSION

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

RUN curl -L -o docker.tgz https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz \
  && tar zxvf docker.tgz \
  && install -o root -g root -m 755 docker/docker /usr/local/bin/docker \
  && rm -rf docker docker.tgz \
  && curl -L -o /usr/local/bin/dumb-init https://github.com/Yelp/dumb-init/releases/download/v1.2.2/dumb-init_1.2.2_amd64 \
  && chmod +x /usr/local/bin/dumb-init \
  && adduser --disabled-password --gecos "" --uid 1000 runner \
  && usermod -aG sudo runner \
  && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers

RUN mkdir -p /runner \
  && cd /runner \
  && curl -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz \
  && tar xzf ./runner.tar.gz \
  && rm runner.tar.gz \
  && ./bin/installdependencies.sh \
  && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /runner

USER runner:runner
ENTRYPOINT ["/usr/local/bin/dumb-init", "--"]
CMD ["/runner/entrypoint.sh"]
