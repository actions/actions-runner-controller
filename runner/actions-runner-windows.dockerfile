FROM mcr.microsoft.com/windows/servercore:ltsc2019

SHELL ["powershell", "-Command", "$ErrorActionPreference = 'Stop';$ProgressPreference='silentlyContinue';"]

ARG RUNNER_VERSION=2.298.2

RUN Invoke-WebRequest \
      -Uri 'https://aka.ms/install-powershell.ps1' \
      -OutFile install-powershell.ps1; \
    powershell -ExecutionPolicy Unrestricted -File ./install-powershell.ps1 -AddToPath

RUN Invoke-WebRequest \
      -Uri https://github.com/actions/runner/releases/download/v$env:RUNNER_VERSION/actions-runner-win-x64-$env:RUNNER_VERSION.zip \
      -OutFile runner.zip; \
    Expand-Archive -Path C:/runner.zip -DestinationPath C:/actions-runner; \
    Remove-Item -Path C:\runner.zip; \
    setx /M PATH $(${Env:PATH} + \";${Env:ProgramFiles}\Git\bin\")

ADD runner.ps1 C:/runner.ps1
CMD ["pwsh", "-ExecutionPolicy", "Unrestricted", "-File", ".\\runner.ps1"]
