FROM ubuntu:18.04

RUN apt update \
  && apt install curl ca-certificates -y --no-install-recommends \
  && curl -L -o docker.tgz https://download.docker.com/linux/static/stable/x86_64/docker-19.03.5.tgz \
  && tar zxvf docker.tgz \
  && install -o root -g root -m 755 docker/docker /usr/local/bin/docker \
  && rm -rf docker docker.tgz \
  && adduser --disabled-password --gecos "" --uid 1000 runner

RUN mkdir -p /runner \
  && cd /runner \
  && curl -O -L https://github.com/actions/runner/releases/download/v2.165.0/actions-runner-linux-x64-2.165.0.tar.gz \
  && tar xzf ./actions-runner-linux-x64-2.165.0.tar.gz \
  && rm actions-runner-linux-x64-2.165.0.tar.gz \
  && ./bin/installdependencies.sh

COPY entrypoint.sh /runner

USER runner:runner
ENTRYPOINT ["/runner/entrypoint.sh"]
