{{/*
Expand the name of the chart.
*/}}
{{- define "auto-scaling-runner-set.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "auto-scaling-runner-set.fullname" -}}
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
{{- define "auto-scaling-runner-set.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "auto-scaling-runner-set.labels" -}}
helm.sh/chart: {{ include "auto-scaling-runner-set.chart" . }}
{{ include "auto-scaling-runner-set.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "auto-scaling-runner-set.selectorLabels" -}}
app.kubernetes.io/name: {{ include "auto-scaling-runner-set.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "auto-scaling-runner-set.githubsecret" -}}
  {{- if kindIs "string" .Values.githubConfigSecret }}
    {{- if not (empty .Values.githubConfigSecret) }}
{{- .Values.githubConfigSecret }}
    {{- else}}
{{- fail "Values.githubConfigSecret is required for setting auth with GitHub server." }}
    {{- end }}
  {{- else }}
{{- include "auto-scaling-runner-set.fullname" . }}-github-secret
  {{- end }}
{{- end }}

{{- define "auto-scaling-runner-set.noPermissionServiceAccountName" -}}
{{- include "auto-scaling-runner-set.fullname" . }}-no-permission-service-account
{{- end }}

{{- define "auto-scaling-runner-set.kubeModeRoleName" -}}
{{- include "auto-scaling-runner-set.fullname" . }}-kube-mode-role
{{- end }}

{{- define "auto-scaling-runner-set.kubeModeServiceAccountName" -}}
{{- include "auto-scaling-runner-set.fullname" . }}-kube-mode-service-account
{{- end }}

{{- define "auto-scaling-runner-set.dind-init-container" -}}
{{- range $i, $val := .Values.template.spec.containers -}}
{{- if eq $val.name "runner" -}}
image: {{ $val.image }}
{{- if $val.imagePullSecrets }}
imagePullSecrets:
  {{ $val.imagePullSecrets | toYaml -}}
{{- end }}
command: ["cp"]
args: ["-r", "-v", "/actions-runner/externals/.", "/actions-runner/tmpDir/"]
volumeMounts:
  - name: dind-externals
    mountPath: /actions-runner/tmpDir
{{- end }}
{{- end }}
{{- end }}

{{- define "auto-scaling-runner-set.dind-container" -}}
image: docker:dind
securityContext:
  privileged: true
volumeMounts:
  - name: work
    mountPath: /actions-runner/_work
  - name: dind-cert
    mountPath: /certs/client
  - name: dind-externals
    mountPath: /actions-runner/externals
{{- end }}

{{- define "auto-scaling-runner-set.dind-volume" -}}
- name: dind-cert
  emptyDir: {}
- name: dind-externals
  emptyDir: {}
{{- end }}

{{- define "auto-scaling-runner-set.dind-work-volume" -}}
{{- $createWorkVolume := 1 }}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 -}}
- name: work
      {{- range $key, $val := $volume }}
        {{- if ne $key "name" }}
  {{ $key }}: {{ $val }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- if eq $createWorkVolume 1 }}
- name: work
  emptyDir: {}
  {{- end }}
{{- end }}

{{- define "auto-scaling-runner-set.kubernetes-mode-work-volume" -}}
{{- $createWorkVolume := 1 }}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 -}}
- name: work
      {{- range $key, $val := $volume }}
        {{- if ne $key "name" }}
  {{ $key }}: {{ $val }}
        {{- end }}
      {{- end }}
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

{{- define "auto-scaling-runner-set.non-work-volumes" -}}
  {{- range $i, $volume := .Values.template.spec.volumes }}
    {{- if ne $volume.name "work" }}
- name: {{ $volume.name }}
      {{- range $key, $val := $volume }}
        {{- if ne $key "name" }}
  {{ $key }}: {{ $val }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "auto-scaling-runner-set.non-runner-containers" -}}
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

{{- define "auto-scaling-runner-set.dind-runner-container" -}}
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
  - name: {{ $env.name }}
        {{- range $envKey, $envVal := $env }}
          {{- if ne $envKey "name" }}
    {{ $envKey }}: {{ $envVal | toYaml | nindent 8 }}
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
    {{- end }}
    {{- $mountWork := 1 }}
    {{- $mountDindCert := 1 }}
volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "work" }}
          {{- $mountWork = 0 -}}
        {{- end }}
        {{- if eq $volMount.name "dind-cert" }}
          {{- $mountDindCert = 0 -}}
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
    mountPath: /actions-runner/_work
    {{- end }}
    {{- if $mountDindCert }}
  - name: dind-cert
    mountPath: /certs/client
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}

{{- define "auto-scaling-runner-set.kubernetes-mode-runner-container" -}}
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
    value: /actions-runner/k8s/index.js
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
    {{- $mountWork := 1 }}
volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "work" }}
          {{- $mountWork = 0 -}}
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
    mountPath: /actions-runner/_work
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}