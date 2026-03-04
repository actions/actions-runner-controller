{{- define "runner-mode-dind.runner-container" -}}
name: runner
image: {{ include "runner.image" . | quote }}
command: {{ include "runner.command" . }}
env:
  - {{ include "runner-mode-dind.env-docker-host" . | nindent 4 }}
  - {{ include "runner-mode-dind.env-wait-for-docker-timeout" . | nindent 4 }}
  {{/* TODO:: Should we skip DOCKER_HOST and RUNNER_WAIT_FOR_DOCKER_IN_SECONDS? */}}
  {{- with .Values.runner.env }}
    {{- toYaml . | nindent 2 }}
  {{- end }}
  {{ include "githubServerTLS.envItems" (dict "root" $ "existingEnv" (.Values.runner.env | default list)) | nindent 2 }}
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  - name: dind-sock
    mountPath: {{ include "runner-mode-dind.sock-mount-dir" . | quote }}
  {{ include "githubServerTLS.volumeMountItem" (dict "root" $ "existingVolumeMounts" (list)) | nindent 2 }}
{{- end }}

{{- define "runner-mode-dind.dind-container" -}}
{{- $dind := .Values.runner.dind | default dict -}}
{{- $dindContainer := ($dind.container | default dict) -}}
{{- if and (hasKey $dind "container") (not (kindIs "map" $dindContainer)) -}}
  {{- fail "runner.dind.container must be a map/object" -}}
{{- end -}}
{{- if and (hasKey $dindContainer "env") (not (kindIs "slice" $dindContainer.env)) -}}
  {{- fail "runner.dind.container.env must be a list" -}}
{{- end -}}
{{- if and (hasKey $dindContainer "volumeMounts") (not (kindIs "slice" $dindContainer.volumeMounts)) -}}
  {{- fail "runner.dind.container.volumeMounts must be a list" -}}
{{- end -}}
{{- if hasKey $dindContainer "volumes" -}}
  {{- fail "runner.dind.container.volumes is not supported; use runner.pod.spec.volumes" -}}
{{- end -}}
{{- if and (hasKey $dindContainer "args") (not (kindIs "slice" $dindContainer.args)) -}}
  {{- fail "runner.dind.container.args must be a list" -}}
{{- end -}}
{{- if and (hasKey $dindContainer "securityContext") (not (kindIs "map" $dindContainer.securityContext)) -}}
  {{- fail "runner.dind.container.securityContext must be a map/object" -}}
{{- end -}}
{{- if and (hasKey $dindContainer "startupProbe") (not (kindIs "map" $dindContainer.startupProbe)) -}}
  {{- fail "runner.dind.container.startupProbe must be a map/object" -}}
{{- end -}}

name: {{ $dindContainer.name | default "dind" }}
image: {{ $dindContainer.image | default "docker:dind" | quote }}
args:
  {{- if $dindContainer.args }}
  {{- toYaml $dindContainer.args | nindent 2 }}
  {{- else }}
  {{- include "runner-mode-dind.args" . | nindent 2 }}
  {{- end }}
env:
  - name: DOCKER_GROUP_GID
    value: {{ ($dind.dockerGroupId | default "123") | quote }}
  {{- with $dindContainer.env }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
securityContext:
{{- if $dindContainer.securityContext }}
  {{- toYaml $dindContainer.securityContext | nindent 2 }}
{{ else }}
  {{- toYaml (dict "privileged" true) | nindent 2 }}
{{- end }}
restartPolicy: Always
startupProbe:
  {{- if $dindContainer.startupProbe }}
  {{- toYaml $dindContainer.startupProbe | nindent 2 }}
  {{- else }}
  {{- include "runner-mode-dind.startup-probe" . | nindent 2 }}
  {{- end }}
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  - name: dind-sock
    mountPath: {{ include "runner-mode-dind.sock-mount-dir" . | quote }}
  {{- with $dindContainer.volumeMounts }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{- if $dind.copyExternals }}
  - name: dind-externals
    mountPath: /home/runner/externals
{{ end }}

{{- $extra := omit $dindContainer "name" "image" "args" "env" "securityContext" "startupProbe" "volumeMounts" -}}
{{- if not (empty $extra) -}}
{{ toYaml $extra }}
{{- end -}}
{{- end }}

{{- define "runner-mode-dind.pod-volumes" -}}
- name: work
  emptyDir: {}
- name: dind-sock
  emptyDir: {}
{{ include "githubServerTLS.podVolumeItem" . }}
{{- if .Values.runner.dind.copyExternals }}
- name: dind-externals
  emptyDir: {}
{{- end }} 
{{- end }}

{{- define "runner-mode-dind.copy-externals" -}}
name: init-dind-externals
image: ghcr.io/actions/actions-runner:latest
command: ["cp", "-r", "/home/runner/externals/.", "/home/runner/tmpDir/"]
volumeMounts:
  - name: dind-externals
    mountPath: /home/runner/tmpDir
{{- end }}

{{- define "runner-mode-dind.startup-probe" -}}
exec:
  command:
    - docker
    - info
initialDelaySeconds: 0
failureThreshold: 24
periodSeconds: 5
{{- end }}

{{- define "runner-mode-dind.args" -}}
{{- $dind := .Values.runner.dind | default dict -}}
{{- $dockerSock := $dind.dockerSock | default "unix:///var/run/docker.sock" -}}
{{- if not (kindIs "string" $dockerSock) -}}
  {{- fail "runner.dind.dockerSock must be a string" -}}
{{- end -}}
- dockerd
- {{ printf "--host=%s" $dockerSock }}
- --group=$(DOCKER_GROUP_GID)
{{- end }}

{{- define "runner-mode-dind.env-docker-host" -}}
{{- $dind := .Values.runner.dind | default dict -}}
{{- $dockerSock := $dind.dockerSock | default "unix:///var/run/docker.sock" -}}
{{- if not (kindIs "string" $dockerSock) -}}
  {{- fail "runner.dind.dockerSock must be a string" -}}
{{- end -}}
name: DOCKER_HOST
value: {{ $dockerSock | quote }}
{{- end }}

{{- define "runner-mode-dind.env-wait-for-docker-timeout" -}}
{{- $dind := .Values.runner.dind | default dict -}}
{{- $waitForDockerInSeconds := $dind.waitForDockerInSeconds | default 120 -}}
{{- if not (or (kindIs "int" $waitForDockerInSeconds) (kindIs "int64" $waitForDockerInSeconds) (kindIs "float64" $waitForDockerInSeconds)) -}}
  {{- fail "runner.dind.waitForDockerInSeconds must be a number" -}}
{{- end -}}
{{- $waitForDockerInSecondsInt := ($waitForDockerInSeconds | int) -}}
{{- if lt $waitForDockerInSecondsInt 0 -}}
    {{- fail "runner.dind.waitForDockerInSeconds must be non-negative" -}}
{{- end -}}
name: RUNNER_WAIT_FOR_DOCKER_IN_SECONDS
value: {{ $waitForDockerInSecondsInt | toString | quote }}
{{- end }}

{{- define "runner-mode-dind.sock-mount-dir" -}}
{{- $dind := .Values.runner.dind | default dict -}}
{{- $dockerSock := $dind.dockerSock | default "unix:///var/run/docker.sock" -}}
{{- if not (kindIs "string" $dockerSock) -}}
  {{- fail "runner.dind.dockerSock must be a string" -}}
{{- end -}}
{{- $dockerSockPath := trimPrefix "unix://" $dockerSock -}}
{{- dir $dockerSockPath -}}
{{- end }}