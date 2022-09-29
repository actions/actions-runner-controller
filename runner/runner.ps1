.\actions-runner\config.cmd --unattended --replace --url https://github.com/${env:RUNNER_REPO} --token $env:RUNNER_TOKEN --name $env:RUNNER_NAME --work $env:RUNNER_WORKDIR;
.\actions-runner\run.cmd;
