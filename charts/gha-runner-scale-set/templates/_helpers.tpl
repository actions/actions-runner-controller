{{/*
Expand the name of the chart.
*/}}

{{- define "gha-base-name" -}}
gha-rs
{{- end }}

{{- define "gha-runner-scale-set.name" -}}
{{- default (include "gha-base-name" .) .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gha-runner-scale-set.scale-set-name" -}}
{{ .Values.runnerScaleSetName | default .Release.Name }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gha-runner-scale-set.fullname" -}}
{{- $name := default (include "gha-base-name" .) }}
{{- printf "%s-%s" (include "gha-runner-scale-set.scale-set-name" .) $name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gha-runner-scale-set.chart" -}}
{{- printf "%s-%s" (include "gha-base-name" .) .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
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
app.kubernetes.io/part-of: gha-rs
actions.github.com/scale-set-name: {{ include "gha-runner-scale-set.scale-set-name" . }}
actions.github.com/scale-set-namespace: {{ include "gha-runner-scale-set.namespace" . }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gha-runner-scale-set.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gha-runner-scale-set.scale-set-name" . }}
app.kubernetes.io/instance: {{ include "gha-runner-scale-set.scale-set-name" . }}
{{- end }}

{{/*
Render a labels or annotations map with all values coerced to strings.
Kubernetes only accepts string values, so scalars such as `true` or `1` must not be
rendered as YAML booleans or numbers.
*/}}
{{- define "gha-runner-scale-set.stringMap" -}}
{{- $out := dict -}}
{{- range $k, $v := . -}}
{{- $_ := set $out $k (printf "%v" $v) -}}
{{- end -}}
{{- toYaml $out -}}
{{- end }}

{{/*
Validate a label or annotation key against the Kubernetes qualified name rules.
Expects a dict with "key", "kind" (label|annotation) and "path" (the values path used in the error message).
*/}}
{{- define "gha-runner-scale-set.validateMetadataKey" -}}
{{- $key := .key -}}
{{- $parts := splitList "/" $key -}}
{{- $name := $key -}}
{{- if gt (len $parts) 2 -}}
{{- fail (printf "%s: invalid %s key %q: a qualified name must consist of an optional DNS subdomain prefix followed by a single '/'" .path .kind $key) -}}
{{- end -}}
{{- if eq (len $parts) 2 -}}
{{- $prefix := index $parts 0 -}}
{{- $name = index $parts 1 -}}
{{- if or (eq $prefix "") (gt (len $prefix) 253) -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain of no more than 253 characters" .path .kind $key $prefix) -}}
{{- end -}}
{{- range $_, $seg := splitList "." $prefix -}}
{{- if or (eq $seg "") (gt (len $seg) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $seg)) -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain of no more than 253 characters" .path .kind $key $prefix) -}}
{{- end -}}
{{- end -}}
{{- if or (eq $name "") (gt (len $name) 63) (not (regexMatch "^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$" $name)) -}}
{{- fail (printf "%s: invalid %s key %q: the name part must be no more than 63 characters, consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" .path .kind $key) -}}
{{- end -}}
{{- end }}

{{/*
Validate a map of labels. Invalid labels are only rejected once the controller creates the
runner pod, which leaves the scale set without runners, so fail at render time instead.
Expects a dict with "labels" and "path".
*/}}
{{- define "gha-runner-scale-set.validateLabels" -}}
{{- $path := .path -}}
{{- range $key, $value := (.labels | default dict) -}}
{{- include "gha-runner-scale-set.validateMetadataKey" (dict "key" $key "kind" "label" "path" $path) -}}
{{- $rendered := printf "%v" $value -}}
{{- if gt (len $rendered) 63 -}}
{{- fail (printf "%s: invalid value %q for label %q: a label value must be no more than 63 characters" $path $rendered $key) -}}
{{- end -}}
{{- if not (regexMatch "^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$" $rendered) -}}
{{- fail (printf "%s: invalid value %q for label %q: a valid label value must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" $path $rendered $key) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Validate a map of annotations. Annotation values are unconstrained, so only keys are validated.
Expects a dict with "annotations" and "path".
*/}}
{{- define "gha-runner-scale-set.validateAnnotations" -}}
{{- $path := .path -}}
{{- range $key, $value := (.annotations | default dict) -}}
{{- include "gha-runner-scale-set.validateMetadataKey" (dict "key" $key "kind" "annotation" "path" $path) -}}
{{- end -}}
{{- end }}

{{/*
Validate every label and annotation map the chart can render onto resources it manages.
*/}}
{{- define "gha-runner-scale-set.validateMetadata" -}}
{{- include "gha-runner-scale-set.validateLabels" (dict "labels" .Values.labels "path" ".Values.labels") -}}
{{- include "gha-runner-scale-set.validateAnnotations" (dict "annotations" .Values.annotations "path" ".Values.annotations") -}}
{{- with .Values.template }}
{{- with .metadata }}
{{- include "gha-runner-scale-set.validateLabels" (dict "labels" .labels "path" ".Values.template.metadata.labels") -}}
{{- include "gha-runner-scale-set.validateAnnotations" (dict "annotations" .annotations "path" ".Values.template.metadata.annotations") -}}
{{- end }}
{{- end }}
{{- range $resource, $meta := (.Values.resourceMeta | default dict) }}
{{- if kindIs "map" $meta }}
{{- include "gha-runner-scale-set.validateLabels" (dict "labels" (index $meta "labels") "path" (printf ".Values.resourceMeta.%s.labels" $resource)) -}}
{{- include "gha-runner-scale-set.validateAnnotations" (dict "annotations" (index $meta "annotations") "path" (printf ".Values.resourceMeta.%s.annotations" $resource)) -}}
{{- end }}
{{- end }}
{{- end }}

{{/*
Render a ResourceMeta block for AutoscalingRunnerSet spec fields.
*/}}
{{- define "gha-runner-scale-set.resourceMetaSpec" -}}
{{- with .labels }}
labels:
  {{- include "gha-runner-scale-set.stringMap" . | nindent 2 }}
{{- end }}
{{- with .annotations }}
annotations:
  {{- include "gha-runner-scale-set.stringMap" . | nindent 2 }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.githubsecret" -}}
  {{- if kindIs "string" .Values.githubConfigSecret }}
    {{- if not (empty .Values.githubConfigSecret) }}
{{- .Values.githubConfigSecret }}
    {{- else}}
{{- fail "Values.githubConfigSecret is required for setting auth with GitHub server." }}
    {{- end }}
  {{- else }}
{{- include "gha-runner-scale-set.fullname" . | replace "_" "-" }}-github-secret
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.noPermissionServiceAccountName" -}}
{{- include "gha-runner-scale-set.fullname" . | replace "_" "-" }}-no-permission
{{- end }}

{{- define "gha-runner-scale-set.kubeModeRoleName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.kubeModeRoleBindingName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.kubeModeServiceAccountName" -}}
{{- include "gha-runner-scale-set.fullname" . | replace "_" "-" }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.dind-init-container" -}}
{{- range $i, $val := .Values.template.spec.containers }}
  {{- if eq $val.name "runner" }}
image: {{ $val.image }}
command: ["cp"]
args: ["-r", "/home/runner/externals/.", "/home/runner/tmpDir/"]
volumeMounts:
  - name: dind-externals
    mountPath: /home/runner/tmpDir
  {{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.dind-container" -}}
image: docker:dind
args:
  - dockerd
  - --host=unix:///var/run/docker.sock
  - --group=$(DOCKER_GROUP_GID)
env:
  - name: DOCKER_GROUP_GID
    value: "123"
securityContext:
  privileged: true
{{- if (ge (.Capabilities.KubeVersion.Minor | int) 29) }}
restartPolicy: Always
startupProbe:
  exec:
    command:
      - docker
      - info
  initialDelaySeconds: 0
  failureThreshold: 24
  periodSeconds: 5
{{- end }}
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  - name: dind-sock
    mountPath: /var/run
  - name: dind-externals
    mountPath: /home/runner/externals
{{- end }}

{{- define "gha-runner-scale-set.dind-volume" -}}
- name: dind-sock
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
      {{- $createWorkVolume = 0 }}
- {{ $volume | toYaml | nindent 2 | trim }}
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
      {{- $createWorkVolume = 0 }}
- {{ $volume | toYaml | nindent 2 | trim  }}
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
- {{ $volume | toYaml | nindent 2 | trim }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.non-runner-containers" -}}
  {{- range $i, $container := .Values.template.spec.containers }}
    {{- if ne $container.name "runner" }}
- {{ $container | toYaml | nindent 2 }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.non-runner-non-dind-containers" -}}
  {{- range $i, $container := .Values.template.spec.containers }}
    {{- if and (ne $container.name "runner") (ne $container.name "dind") }}
- {{ $container | toYaml | nindent 2 }}
    {{- end }}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.dind-runner-container" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers }}
  {{- if eq $container.name "runner" }}
    {{- range $key, $val := $container }}
      {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
{{ $key }}: {{ $val | toYaml | nindent 2 }}
      {{- end }}
    {{- end }}
    {{- $setDockerHost := 1 }}
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
          {{- $setDockerHost = 0 }}
        {{- end }}
        {{- if eq $env.name "RUNNER_WAIT_FOR_DOCKER_IN_SECONDS" }}
          {{- $setRunnerWaitDocker = 0 }}
        {{- end }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 }}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 }}
        {{- end }}
  - {{ $env | toYaml | nindent 4 | trim }}
      {{- end }}
    {{- end }}
    {{- if $setDockerHost }}
  - name: DOCKER_HOST
    value: unix:///var/run/docker.sock
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
          {{- $mountWork = 0 }}
        {{- end }}
        {{- if eq $volMount.name "dind-sock" }}
          {{- $mountDindCert = 0 }}
        {{- end }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 }}
        {{- end }}
  - {{ $volMount | toYaml | nindent 4 | trim }}
      {{- end }}
    {{- end }}
    {{- if $mountWork }}
  - name: work
    mountPath: /home/runner/_work
    {{- end }}
    {{- if $mountDindCert }}
  - name: dind-sock
    mountPath: /var/run
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
{{- range $i, $container := .Values.template.spec.containers }}
  {{- if eq $container.name "runner" }}
    {{- range $key, $val := $container }}
      {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
{{ $key }}: {{ $val | toYaml | nindent 2 }}
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
          {{- $setContainerHooks = 0 }}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_POD_NAME" }}
          {{- $setPodName = 0 }}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER" }}
          {{- $setRequireJobContainer = 0 }}
        {{- end }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 }}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 }}
        {{- end }}
  - {{ $env | toYaml | nindent 4 }}
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
          {{- $mountWork = 0 }}
        {{- end }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 }}
        {{- end }}
  - {{ $volMount | toYaml | nindent 4 }}
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

{{- define "gha-runner-scale-set.kubernetes-novolume-mode-runner-container" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers }}
  {{- if eq $container.name "runner" }}
    {{- $setRunnerImage := "" }}
    {{- range $key, $val := $container }}
      {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
      {{- if eq $key "image" }}
        {{- $setRunnerImage = $val }}
      {{- end }}
{{ $key }}: {{ $val | toYaml | nindent 2 }}
      {{- end }}
    {{- end }}
    {{- $setContainerHooks := 1 }}
    {{- $setPodName := 1 }}
    {{- $setRequireJobContainer := 1 }}
    {{- $setActionsRunnerImage := 1 }}
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
          {{- $setContainerHooks = 0 }}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_IMAGE" }}
          {{- $setActionsRunnerImage = 0 }}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_POD_NAME" }}
          {{- $setPodName = 0 }}
        {{- end }}
        {{- if eq $env.name "ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER" }}
          {{- $setRequireJobContainer = 0 }}
        {{- end }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 }}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 }}
        {{- end }}
  - {{ $env | toYaml | nindent 4 }}
      {{- end }}
    {{- end }}
    {{- if $setContainerHooks }}
  - name: ACTIONS_RUNNER_CONTAINER_HOOKS
    value: /home/runner/k8s-novolume/index.js
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
    {{- if $setActionsRunnerImage }}
  - name: ACTIONS_RUNNER_IMAGE
    value: "{{- $setRunnerImage -}}"
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
    {{- if or $container.volumeMounts $mountGitHubServerTLS }}
volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 }}
        {{- end }}
  - {{ $volMount | toYaml | nindent 4 }}
      {{- end }}
    {{- end }}
    {{- if $mountGitHubServerTLS }}
  - name: github-server-tls-cert
    mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
    {{- end }}
    {{- else }}
volumeMounts: []
    {{- end }}
  {{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.default-mode-runner-containers" -}}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- range $i, $container := .Values.template.spec.containers }}
{{- if ne $container.name "runner" }}
- {{ $container | toYaml | nindent 2 }}
{{- else }}
- name: {{ $container.name }}
  {{- range $key, $val := $container }}
    {{- if and (ne $key "env") (ne $key "volumeMounts") (ne $key "name") }}
  {{ $key }}: {{ $val | toYaml | nindent 4 }}
    {{- end }}
  {{- end }}
  {{- $setNodeExtraCaCerts := 0 }}
  {{- $setRunnerUpdateCaCerts := 0 }}
  {{- if $tlsConfig.runnerMountPath }}
    {{- $setNodeExtraCaCerts = 1 }}
    {{- $setRunnerUpdateCaCerts = 1 }}
  {{- end }}

  {{- $mountGitHubServerTLS := 0 }}
  {{- if or $container.env $setNodeExtraCaCerts $setRunnerUpdateCaCerts }}
  env:
    {{- with $container.env }}
      {{- range $i, $env := . }}
        {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
          {{- $setNodeExtraCaCerts = 0 }}
        {{- end }}
        {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
          {{- $setRunnerUpdateCaCerts = 0 }}
        {{- end }}
    - {{ $env | toYaml | nindent 6 }}
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
    {{- if $tlsConfig.runnerMountPath }}
      {{- $mountGitHubServerTLS = 1 }}
    {{- end }}
  {{- end }}

  {{- if or $container.volumeMounts $mountGitHubServerTLS }}
  volumeMounts:
    {{- with $container.volumeMounts }}
      {{- range $i, $volMount := . }}
        {{- if eq $volMount.name "github-server-tls-cert" }}
          {{- $mountGitHubServerTLS = 0 }}
        {{- end }}
    - {{ $volMount | toYaml | nindent 6 }}
      {{- end }}
    {{- end }}
    {{- if $mountGitHubServerTLS }}
    - name: github-server-tls-cert
      mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
      subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
    {{- end }}
  {{- end}}
{{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.managerRoleName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-manager
{{- end }}

{{- define "gha-runner-scale-set.managerRoleBindingName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-manager
{{- end }}

{{- define "gha-runner-scale-set.managerServiceAccountName" -}}
{{- $searchControllerDeployment := 1 }}
{{- if .Values.controllerServiceAccount }}
  {{- if .Values.controllerServiceAccount.name }}
    {{- $searchControllerDeployment = 0 }}
{{- .Values.controllerServiceAccount.name }}
  {{- end }}
{{- end }}
{{- if eq $searchControllerDeployment 1 }}
  {{- $multiNamespacesCounter := 0 }}
  {{- $singleNamespaceCounter := 0 }}
  {{- $controllerDeployment := dict }}
  {{- $singleNamespaceControllerDeployments := dict }}
  {{- $managerServiceAccountName := "" }}
  {{- range $index, $deployment := (lookup "apps/v1" "Deployment" "" "").items }}
    {{- if kindIs "map" $deployment.metadata.labels }}
      {{- if eq (get $deployment.metadata.labels "app.kubernetes.io/part-of") "gha-rs-controller" }}
        {{- if hasKey $deployment.metadata.labels "actions.github.com/controller-watch-single-namespace" }}
          {{- $singleNamespaceCounter = add $singleNamespaceCounter 1 }}
          {{- $_ := set $singleNamespaceControllerDeployments (get $deployment.metadata.labels "actions.github.com/controller-watch-single-namespace") $deployment}}
        {{- else }}
          {{- $multiNamespacesCounter = add $multiNamespacesCounter 1 }}
          {{- $controllerDeployment = $deployment }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- if and (eq $multiNamespacesCounter 0) (eq $singleNamespaceCounter 0) }}
    {{- fail "No gha-rs-controller deployment found using label (app.kubernetes.io/part-of=gha-rs-controller). Consider setting controllerServiceAccount.name in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if and (gt $multiNamespacesCounter 0) (gt $singleNamespaceCounter 0) }}
    {{- fail "Found both gha-rs-controller installed with flags.watchSingleNamespace set and unset in cluster, this is not supported. Consider setting controllerServiceAccount.name in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if gt $multiNamespacesCounter 1 }}
    {{- fail "More than one gha-rs-controller deployment found using label (app.kubernetes.io/part-of=gha-rs-controller). Consider setting controllerServiceAccount.name in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if eq $multiNamespacesCounter 1 }}
    {{- with $controllerDeployment.metadata }}
      {{- $managerServiceAccountName = (get $controllerDeployment.metadata.labels "actions.github.com/controller-service-account-name") }}
    {{- end }}
  {{- else if gt $singleNamespaceCounter 0 }}
    {{- if hasKey $singleNamespaceControllerDeployments (include "gha-runner-scale-set.namespace" .) }}
      {{- $controllerDeployment = get $singleNamespaceControllerDeployments (include "gha-runner-scale-set.namespace" .) }}
      {{- with $controllerDeployment.metadata }}
        {{- $managerServiceAccountName = (get $controllerDeployment.metadata.labels "actions.github.com/controller-service-account-name") }}
      {{- end }}
    {{- else }}
      {{- fail "No gha-rs-controller deployment that watch this namespace found using label (actions.github.com/controller-watch-single-namespace). Consider setting controllerServiceAccount.name in values.yaml to be explicit if you think the discovery is wrong." }}
    {{- end }}
  {{- end }}
  {{- if eq $managerServiceAccountName "" }}
    {{- fail "No service account name found for gha-rs-controller deployment using label (actions.github.com/controller-service-account-name), consider setting controllerServiceAccount.name in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
{{- $managerServiceAccountName }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.managerServiceAccountNamespace" -}}
{{- $searchControllerDeployment := 1 }}
{{- if .Values.controllerServiceAccount }}
  {{- if .Values.controllerServiceAccount.namespace }}
    {{- $searchControllerDeployment = 0 }}
{{- .Values.controllerServiceAccount.namespace }}
  {{- end }}
{{- end }}
{{- if eq $searchControllerDeployment 1 }}
  {{- $multiNamespacesCounter := 0 }}
  {{- $singleNamespaceCounter := 0 }}
  {{- $controllerDeployment := dict }}
  {{- $singleNamespaceControllerDeployments := dict }}
  {{- $managerServiceAccountNamespace := "" }}
  {{- range $index, $deployment := (lookup "apps/v1" "Deployment" "" "").items }}
    {{- if kindIs "map" $deployment.metadata.labels }}
      {{- if eq (get $deployment.metadata.labels "app.kubernetes.io/part-of") "gha-rs-controller" }}
        {{- if hasKey $deployment.metadata.labels "actions.github.com/controller-watch-single-namespace" }}
          {{- $singleNamespaceCounter = add $singleNamespaceCounter 1 }}
          {{- $_ := set $singleNamespaceControllerDeployments (get $deployment.metadata.labels "actions.github.com/controller-watch-single-namespace") $deployment}}
        {{- else }}
          {{- $multiNamespacesCounter = add $multiNamespacesCounter 1 }}
          {{- $controllerDeployment = $deployment }}
        {{- end }}
      {{- end }}
    {{- end }}
  {{- end }}
  {{- if and (eq $multiNamespacesCounter 0) (eq $singleNamespaceCounter 0) }}
    {{- fail "No gha-rs-controller deployment found using label (app.kubernetes.io/part-of=gha-rs-controller). Consider setting controllerServiceAccount.namespace in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if and (gt $multiNamespacesCounter 0) (gt $singleNamespaceCounter 0) }}
    {{- fail "Found both gha-rs-controller installed with flags.watchSingleNamespace set and unset in cluster, this is not supported. Consider setting controllerServiceAccount.namespace in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if gt $multiNamespacesCounter 1 }}
    {{- fail "More than one gha-rs-controller deployment found using label (app.kubernetes.io/part-of=gha-rs-controller). Consider setting controllerServiceAccount.namespace in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
  {{- if eq $multiNamespacesCounter 1 }}
    {{- with $controllerDeployment.metadata }}
      {{- $managerServiceAccountNamespace = (get $controllerDeployment.metadata.labels "actions.github.com/controller-service-account-namespace") }}
    {{- end }}
  {{- else if gt $singleNamespaceCounter 0 }}
    {{- if hasKey $singleNamespaceControllerDeployments (include "gha-runner-scale-set.namespace" .) }}
      {{- $controllerDeployment = get $singleNamespaceControllerDeployments (include "gha-runner-scale-set.namespace" .) }}
      {{- with $controllerDeployment.metadata }}
        {{- $managerServiceAccountNamespace = (get $controllerDeployment.metadata.labels "actions.github.com/controller-service-account-namespace") }}
      {{- end }}
    {{- else }}
      {{- fail "No gha-rs-controller deployment that watch this namespace found using label (actions.github.com/controller-watch-single-namespace). Consider setting controllerServiceAccount.namespace in values.yaml to be explicit if you think the discovery is wrong." }}
    {{- end }}
  {{- end }}
  {{- if eq $managerServiceAccountNamespace "" }}
    {{- fail "No service account namespace found for gha-rs-controller deployment using label (actions.github.com/controller-service-account-namespace), consider setting controllerServiceAccount.namespace in values.yaml to be explicit if you think the discovery is wrong." }}
  {{- end }}
{{- $managerServiceAccountNamespace }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.namespace" -}}
{{- if .Values.namespaceOverride }}
  {{- .Values.namespaceOverride }}
{{- else }}
  {{- .Release.Namespace }}
{{- end }}
{{- end }}
