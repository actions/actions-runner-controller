FROM ubuntu:18.04

ARG RUNNER_VERSION

ENV container=docker

RUN apt update \
  && apt install -y systemd apt-transport-https ca-certificates curl gnupg-agent software-properties-common \
  && systemctl set-default multi-user.target \
  && systemctl mask systemd-logind.service systemd-timesyncd.service systemd-resolved.service systemd-networkd.service \
  && systemctl mask unattended-upgrades.service networkd-dispatcher.service cron.service dev-ttyS0.device \
  && systemctl disable apt-daily.timer apt-daily-upgrade.timer \
  && curl -fsSL https://download.docker.com/linux/ubuntu/gpg | apt-key add - \
  && add-apt-repository "deb [arch=amd64] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
  && apt update \
  && apt install -y docker-ce docker-ce-cli containerd.io \
  && rm -rf /var/lib/apt/lists/*

RUN mkdir -p /runner \
  && cd /runner \
  && curl -L -o runner.tar.gz https://github.com/actions/runner/releases/download/v${RUNNER_VERSION}/actions-runner-linux-x64-${RUNNER_VERSION}.tar.gz \
  && tar xzf ./runner.tar.gz \
  && rm runner.tar.gz \
  && ./bin/installdependencies.sh \
  && useradd -u 1000 -G sudo,docker -s /sbin/nologin -M runner \
  && echo "%sudo   ALL=(ALL:ALL) NOPASSWD:ALL" > /etc/sudoers \
  && chown -R runner:runner /runner

COPY files /tmp/files

RUN cp /tmp/files/entrypoint.sh /usr/local/bin/entrypoint \
  && cp /tmp/files/actions-runner.sh /usr/local/bin/actions-runner \
  && cp /tmp/files/actions-runner-halt.sh /usr/local/bin/actions-runner-halt \
  && cp /tmp/files/actions-runner.service /etc/systemd/system/actions-runner.service \
  && systemctl enable actions-runner.service \
  && rm -rf /tmp/files /var/log/* /tmp/* /usr/share/doc* /usr/share/man/* /usr/share/local/*

STOPSIGNAL 37

CMD ["/usr/local/bin/entrypoint"]
