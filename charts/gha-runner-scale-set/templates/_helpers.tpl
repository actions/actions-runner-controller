{{/*
Expand the name of the chart.
*/}}
{{- define "gha-runner-scale-set.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gha-runner-scale-set.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gha-runner-scale-set.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gha-runner-scale-set.labels" -}}
helm.sh/chart: {{ include "gha-runner-scale-set.chart" . }}
{{ include "gha-runner-scale-set.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gha-runner-scale-set.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gha-runner-scale-set.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gha-runner-scale-set.githubsecret" -}}
  {{- if kindIs "string" .Values.githubConfigSecret }}
    {{- if not (empty .Values.githubConfigSecret) }}
{{- .Values.githubConfigSecret }}
    {{- else}}
{{- fail "Values.githubConfigSecret is required for setting auth with GitHub server." }}
    {{- end }}
  {{- else }}
{{- include "gha-runner-scale-set.fullname" . }}-github-secret
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.noPermissionServiceAccountName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-no-permission-service-account
{{- end }}

{{- define "gha-runner-scale-set.kubeModeRoleName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode-role
{{- end }}

{{- define "gha-runner-scale-set.kubeModeServiceAccountName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode-service-account
{{- end }}

{{- define "gha-runner-scale-set.dind-init-container" -}}
{{- range $i, $val := .Values.template.spec.containers -}}
{{- if eq $val.name "runner" -}}
image: {{ $val.image }}
{{- if $val.imagePullSecrets }}
imagePullSecrets:
  {{ $val.imagePullSecrets | toYaml -}}
{{- end }}
command: ["cp"]
args: ["-r", "-v", "/home/runner/externals/.", "/home/runner/tmpDir/"]
volumeMounts:
  - name: dind-externals
    mountPath: /home/runner/tmpDir
{{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.dind-container" -}}
image: docker:dind
securityContext:
  privileged: true
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  - name: dind-cert
    mountPath: /certs/client
  - name: dind-externals
    mountPath: /home/runner/externals
{{- end }}

{{- define "gha-runner-scale-set.dind-volume" -}}
- name: dind-cert
  emptyDir: {}
- name: dind-externals
  emptyDir: {}
{{- end }}

{{- define "gha-runner-scale-set.tls-volume" -}}
- name: github-server-tls-cert
  configMap:
    name: {{ .certificateFrom.configMapKeyRef.name }}
    items:
      - key: {{ .certificateFrom.configMapKeyRef.key }}
        path: {{ .certificateFrom.configMapKeyRef.key }}
{{- end }}

{{- define "gha-runner-scale-set.dind-work-volume" -}}
{{- $createWorkVolume := 1 }}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 -}}
- {{ $volume | toYaml | nindent 2 }}
    {{- end }}
  {{- end }}
  {{- if eq $createWorkVolume 1 }}
- name: work
  emptyDir: {}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.kubernetes-mode-work-volume" -}}
{{- $createWorkVolume := 1 }}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 -}}
- {{ $volume | toYaml | nindent 2 }}
    {{- end }}
  {{- end }}
  {{- if eq $createWorkVolume 1 }}
- name: work
  ephemeral:
    volumeClaimTemplate:
      spec:
        {{- .Values.containerMode.kubernetesModeWorkVolumeClaim | toYaml | nindent 8 }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.non-work-volumes" -}}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if ne $volume.name "work" }}
- {{ $volume | toYaml | nindent 2 }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.non-runner-containers" -}}
  {{- range $i, $container := .Values.template.spec.containers -}}
    {{- if ne $container.name "runner" -}}
- name: {{ $container.name }}
      {{- range $key, $val := $container }}
        {{- if ne $key "name" }}
  {{ $key }}: {{ $val }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.dind-runner-container" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers -}}
  {{- if eq $container.name "runner" -}}
    {{- range $key, $val := $container }}
      {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
{{ $key }}: {{ $val }}
      {{- end }}
    {{- end }}
    {{- $setDockerHost := 1 }}
    {{- $setDockerTlsVerify := 1 }}
    {{- $setDockerCertPath := 1 }}
    {{- $setRunnerWaitDocker := 1 }}
    {{- $setNodeExtraCaCerts := 0 }}
    {{- $setRunnerUpdateCaCerts := 0 }}
    {{- if $tlsConfig.runnerMountPath }}
      {{- $setNodeExtraCaCerts = 1 }}
      {{- $setRunnerUpdateCaCerts = 1 }}
    {{- end }}
env:
    {{- with $container.env }}
      {{- range $i, $env := . }}
        {{- if eq $env.name "DOCKER_HOST" }}
          {{- $setDockerHost = 0 -}}
        {{- end }}
        {{- if eq $env.name "DOCKER_TLS_VERIFY" }}
          {{- $setDockerTlsVerify = 0 -}}
        {{- end }}
        {{- if eq $env.name "DOCKER_CERT_PATH" }}
          {{- $setDockerCertPath = 0 -}}
        {{- end }}
        {{- if eq $env.name "RUNNER_WAIT_FOR_DOCKER_IN_SECONDS" }}
          {{- $setRunnerWaitDocker = 0 -}}
        {{- end }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 -}}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 -}}
        {{- end }}
  - name: {{ $env.name }}
        {{- range $envKey, $envVal := $env }}
          {{- if ne $envKey "name" }}
    {{ $envKey }}: {{ $envVal | toYaml | nindent 8 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $setDockerHost }}
  - name: DOCKER_HOST
    value: tcp://localhost:2376
    {{- end }}
    {{- if $setDockerTlsVerify }}
  - name: DOCKER_TLS_VERIFY
    value: "1"
    {{- end }}
    {{- if $setDockerCertPath }}
  - name: DOCKER_CERT_PATH
    value: /certs/client
    {{- end }}
    {{- if $setRunnerWaitDocker }}
  - name: RUNNER_WAIT_FOR_DOCKER_IN_SECONDS
    value: "120"
    {{- end }}
    {{- if $setNodeExtraCaCerts }}
  - name: NODE_EXTRA_CA_CERTS
    value: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    {{- end }}
    {{- if $setRunnerUpdateCaCerts }}
  - name: RUNNER_UPDATE_CA_CERTS
    value: "1"
    {{- end }}
    {{- $mountWork := 1 }}
    {{- $mountDindCert := 1 }}
    {{- $mountGitHubServerTLS := 0 }}
    {{- if $tlsConfig.runnerMountPath }}
      {{- $mountGitHubServerTLS = 1 }}
    {{- end }}
volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "work" }}
          {{- $mountWork = 0 -}}
        {{- end }}
        {{- if eq $volMount.name "dind-cert" }}
          {{- $mountDindCert = 0 -}}
        {{- end }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 -}}
        {{- end }}
  - name: {{ $volMount.name }}
        {{- range $mountKey, $mountVal := $volMount }}
          {{- if ne $mountKey "name" }}
    {{ $mountKey }}: {{ $mountVal | toYaml | nindent 8 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $mountWork }}
  - name: work
    mountPath: /home/runner/_work
    {{- end }}
    {{- if $mountDindCert }}
  - name: dind-cert
    mountPath: /certs/client
    readOnly: true
    {{- end }}
    {{- if $mountGitHubServerTLS }}
  - name: github-server-tls-cert
    mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.kubernetes-mode-runner-container" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers -}}
  {{- if eq $container.name "runner" -}}
    {{- range $key, $val := $container }}
      {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
{{ $key }}: {{ $val }}
      {{- end }}
    {{- end }}
    {{- $setContainerHooks := 1 }}
    {{- $setPodName := 1 }}
    {{- $setRequireJobContainer := 1 }}
    {{- $setNodeExtraCaCerts := 0 }}
    {{- $setRunnerUpdateCaCerts := 0 }}
    {{- if $tlsConfig.runnerMountPath }}
      {{- $setNodeExtraCaCerts = 1 }}
      {{- $setRunnerUpdateCaCerts = 1 }}
    {{- end }}
env:
    {{- with $container.env }}
      {{- range $i, $env := . }}
        {{- if eq $env.name "ACTIONS_RUNNER_CONTAINER_HOOKS" }}
          {{- $setContainerHooks = 0 -}}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_POD_NAME" }}
          {{- $setPodName = 0 -}}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER" }}
          {{- $setRequireJobContainer = 0 -}}
        {{- end }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 -}}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 -}}
        {{- end }}
  - name: {{ $env.name }}
        {{- range $envKey, $envVal := $env }}
          {{- if ne $envKey "name" }}
    {{ $envKey }}: {{ $envVal | toYaml | nindent 8 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $setContainerHooks }}
  - name: ACTIONS_RUNNER_CONTAINER_HOOKS
    value: /home/runner/k8s/index.js
    {{- end }}
    {{- if $setPodName }}
  - name: ACTIONS_RUNNER_POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
    {{- end }}
    {{- if $setRequireJobContainer }}
  - name: ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER
    value: "true"
    {{- end }}
    {{- if $setNodeExtraCaCerts }}
  - name: NODE_EXTRA_CA_CERTS
    value: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    {{- end }}
    {{- if $setRunnerUpdateCaCerts }}
  - name: RUNNER_UPDATE_CA_CERTS
    value: "1"
    {{- end }}
    {{- $mountWork := 1 }}
    {{- $mountGitHubServerTLS := 0 }}
    {{- if $tlsConfig.runnerMountPath }}
      {{- $mountGitHubServerTLS = 1 }}
    {{- end }}
volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "work" }}
          {{- $mountWork = 0 -}}
        {{- end }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 -}}
        {{- end }}
  - name: {{ $volMount.name }}
        {{- range $mountKey, $mountVal := $volMount }}
          {{- if ne $mountKey "name" }}
    {{ $mountKey }}: {{ $mountVal | toYaml | nindent 8 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $mountWork }}
  - name: work
    mountPath: /home/runner/_work
    {{- end }}
    {{- if $mountGitHubServerTLS }}
  - name: github-server-tls-cert
    mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.default-mode-runner-containers" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers -}}
{{- if ne $container.name "runner" -}}
- {{ $container | toYaml | nindent 2 }}
{{- else }}
- name: {{ $container.name }}
  {{- range $key, $val := $container }}
    {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
  {{ $key }}: {{ $val }}
    {{- end }}
  {{- end }}
  {{- $setNodeExtraCaCerts := 0 }}
  {{- $setRunnerUpdateCaCerts := 0 }}
  {{- if $tlsConfig.runnerMountPath }}
    {{- $setNodeExtraCaCerts = 1 }}
    {{- $setRunnerUpdateCaCerts = 1 }}
  {{- end }}
  env:
    {{- with $container.env }}
      {{- range $i, $env := . }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 -}}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 -}}
        {{- end }}
    - name: {{ $env.name }}
        {{- range $envKey, $envVal := $env }}
          {{- if ne $envKey "name" }}
      {{ $envKey }}: {{ $envVal | toYaml | nindent 10 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $setNodeExtraCaCerts }}
    - name: NODE_EXTRA_CA_CERTS
      value: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    {{- end }}
    {{- if $setRunnerUpdateCaCerts }}
    - name: RUNNER_UPDATE_CA_CERTS
      value: "1"
    {{- end }}
    {{- $mountGitHubServerTLS := 0 }}
    {{- if $tlsConfig.runnerMountPath }}
      {{- $mountGitHubServerTLS = 1 }}
    {{- end }}
  volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 -}}
        {{- end }}
    - name: {{ $volMount.name }}
        {{- range $mountKey, $mountVal := $volMount }}
          {{- if ne $mountKey "name" }}
      {{ $mountKey }}: {{ $mountVal | toYaml | nindent 10 }}
          {{- end }}
        {{- end }}
      {{- end }}
    {{- end }}
    {{- if $mountGitHubServerTLS }}
    - name: github-server-tls-cert
      mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
      subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
    {{- end }}
{{- end }}
{{- end }}
{{- end }}
