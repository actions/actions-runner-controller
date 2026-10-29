# Configuring Windows runners

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Setting up Windows Runners

The main two steps in enabling Windows self-hosted runners are:

- Using `nodeSelector`'s property to filter the `cert-manger` and `actions-runner-controller` pods
- Deploying a RunnerDeployment using a Windows-based image

For the first step, you need to set the `nodeSelector.kubernetes.io/os` property in both the `cert-manager` and the `actions-runner-controller` deployments to `linux` so that the pods for these two deployments are only scheduled in Linux nodes. You can do this as follows:

```yaml
nodeSelector:
  kubernetes.io/os: linux
```

`cert-manager` has 4 different application within it the main application, the `webhook`, the `cainjector` and the `startupapicheck`. In the parameters or values file you use for the deployment you need to add the `nodeSelector` property four times, one for each application.

For the `actions-runner-controller` you only have to use the `nodeSelector` only for the main deployment, so it only has to be set once.

Once this is set up, you will need to deploy two different `RunnerDeployment`'s, one for Windows and one for Linux.
The Linux deployment can use either the default image or a custom one, however, there isn't a default Windows image so for Windows deployments you will have to build your own image.

Below we share an example of the YAML used to create the deployment for each Operating System and a Dockerfile for the Windows deployment. 

<details><summary>Windows</summary>
<p>

### RunnerDeployment

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: k8s-runners-windows
  namespace: actions-runner-system
spec:
  template:
    spec:
      image: <repo>/<image>:<windows-tag>
      dockerdWithinRunnerContainer: true
      nodeSelector:
        kubernetes.io/os: windows
        kubernetes.io/arch: amd64
      repository: <owner>/<repo>
      labels:
        - windows
        - X64
```

### Dockerfile

> Note that you'd need to patch the below Dockerfile if you need a graceful termination.
> See https://github.com/actions/actions-runner-controller/pull/1608/files#r917319574 for more information.
  
  
I would like to propose in Dockerfile, instead of lines:

RUN Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v2.292.0/actions-runner-win-x64-2.292.0.zip -OutFile actions-runner-win-x64-2.292.0.zip
RUN if((Get-FileHash -Path actions-runner-win-x64-2.292.0.zip -Algorithm SHA256).Hash.ToUpper() -ne 'f27dae1413263e43f7416d719e0baf338c8d80a366fed849ecf5fffcec1e941f'.ToUpper()){ throw 'Computed checksum did not match' }
RUN Add-Type -AssemblyName System.IO.Compression.FileSystem ; [System.IO.Compression.ZipFile]::ExtractToDirectory('actions-runner-win-x64-2.292.0.zip', $PWD)

To add the following after the powershell installation step:

COPY get-latestrunnerversion.ps1 
RUN ./get-latestrunnerversion.ps1 
  
Where the get-latestrunnerversion.ps1 script looks like this:  
  
$URI="https://api.github.com/repos/actions/runner/releases/latest"
$Version=(Invoke-Webrequest $URI)
$latest=($Version | ConvertFrom-Json).name
$vnum=($latest).Substring(1)
Invoke-WebRequest -Uri "https://github.com/actions/runner/releases/download/$latest/actions-runner-win-x64-$vnum.zip" -OutFile "actions-runner-win-x64-latest.zip"
Expand-Archive -Path $PWD\actions-runner-win-x64-latest.zip -DestinationPath "$PWD" -Force
Remove-Item -Path $PWD\actions-runner-win-x64-latest.zip -Force
  
  
```Dockerfile
FROM mcr.microsoft.com/windows/servercore:ltsc2019

WORKDIR /actions-runner

SHELL ["powershell", "-Command", "$ErrorActionPreference = 'Stop';$ProgressPreference='silentlyContinue';"]

RUN Invoke-WebRequest -Uri https://github.com/actions/runner/releases/download/v2.292.0/actions-runner-win-x64-2.292.0.zip -OutFile actions-runner-win-x64-2.292.0.zip

RUN if((Get-FileHash -Path actions-runner-win-x64-2.292.0.zip -Algorithm SHA256).Hash.ToUpper() -ne 'f27dae1413263e43f7416d719e0baf338c8d80a366fed849ecf5fffcec1e941f'.ToUpper()){ throw 'Computed checksum did not match' }

RUN Add-Type -AssemblyName System.IO.Compression.FileSystem ; [System.IO.Compression.ZipFile]::ExtractToDirectory('actions-runner-win-x64-2.292.0.zip', $PWD)

RUN Invoke-WebRequest -Uri 'https://aka.ms/install-powershell.ps1' -OutFile install-powershell.ps1; ./install-powershell.ps1 -AddToPath

RUN powershell Set-ExecutionPolicy Bypass -Scope Process -Force; [System.Net.ServicePointManager]::SecurityProtocol = [System.Net.ServicePointManager]::SecurityProtocol -bor 3072; iex ((New-Object System.Net.WebClient).DownloadString('https://community.chocolatey.org/install.ps1'))

RUN powershell choco install git.install --params "'/GitAndUnixToolsOnPath'" -y

RUN powershell choco feature enable -n allowGlobalConfirmation

CMD [ "pwsh", "-c", "./config.cmd --name $env:RUNNER_NAME --url https://github.com/$env:RUNNER_REPO --token $env:RUNNER_TOKEN --labels $env:RUNNER_LABELS --unattended --replace --ephemeral; ./run.cmd"]
```
</p>
</details>


<details><summary>Linux</summary>
<p>

### RunnerDeployment

```yaml
---
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: k8s-runners-linux
  namespace: actions-runner-system
spec:
  template:
    spec:
      image: <repo>/<image>:<linux-tag>
      nodeSelector:
        kubernetes.io/os: linux
        kubernetes.io/arch: amd64
      repository: <owner>:<repo>
      labels:
        - linux
        - X64
```
</p>
</details>

After both `RunnerDeployment`'s are up and running, you can now proceed to deploy the `HorizontalRunnerAutoscaler` for each deployment.
