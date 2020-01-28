FROM ubuntu:18.04

RUN apt update \
  && apt install curl ca-certificates expect -y --no-install-recommends \
  && adduser --disabled-password --gecos "" --uid 1001 runner

RUN mkdir -p /runner \
  && cd /runner \
  && curl -O -L https://github.com/actions/runner/releases/download/v2.165.0/actions-runner-linux-x64-2.165.0.tar.gz \
  && tar xzf ./actions-runner-linux-x64-2.165.0.tar.gz \
  && rm actions-runner-linux-x64-2.165.0.tar.gz \
  && ./bin/installdependencies.sh

COPY entrypoint.sh /runner

USER runner:runner
ENTRYPOINT ["/runner/entrypoint.sh"]
