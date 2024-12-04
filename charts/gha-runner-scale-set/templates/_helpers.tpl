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
actions.github.com/scale-set-namespace: {{ .Release.Namespace }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gha-runner-scale-set.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gha-runner-scale-set.scale-set-name" . }}
app.kubernetes.io/instance: {{ include "gha-runner-scale-set.scale-set-name" . }}
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
{{- include "gha-runner-scale-set.fullname" . }}-no-permission
{{- end }}

{{- define "gha-runner-scale-set.kubeModeRoleName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.kubeModeRoleBindingName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.kubeModeServiceAccountName" -}}
{{- include "gha-runner-scale-set.fullname" . }}-kube-mode
{{- end }}

{{- define "gha-runner-scale-set.dind-init-container" -}}
image: {{ (.Values.runner.dindInitContainer).image | default "ghcr.io/actions/actions-runner:latest" }}
imagePullPolicy: {{ .Values.runner.dindInitContainer.imagePullPolicy }}
command: ["cp"]
args:
  - -r
  - -v
  - /home/runner/externals/.
  - /home/runner/tmpDir/
{{- with (.Values.runner.dindInitContainer).extraArgs }}
  {{- toYaml . | nindent 2 -}}
{{- end }}
volumeMounts:
  - name: dind-externals
    mountPath: /home/runner/tmpDir
{{ if not (empty .Values.runner.dindInitContainer.resources) }}
resources:
  {{- toYaml .Values.runner.dindInitContainer.resources | nindent 2 -}}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.dind-container" -}}
image: {{ (.Values.runner.dindContainer).image | default "docker:dind" }}
imagePullPolicy: {{ .Values.runner.dindInitContainer.imagePullPolicy }}
args:
  - dockerd
  - --host=unix:///var/run/docker.sock
  - --group=$(DOCKER_GROUP_GID)
{{- with (.Values.runner.dindContainer).extraArgs -}}
  {{- toYaml . | nindent 2 -}}
{{- end }}
env:
  - name: DOCKER_GROUP_GID
    value: "123"
{{- with (.Values.runner.dindContainer).extraEnv -}}
  {{- toYaml . | nindent 2 -}}
{{- end }}
securityContext:
  privileged: true
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  - name: dind-sock
    mountPath: /var/run
  - name: dind-externals
    mountPath: /home/runner/externals
{{- with (.Values.runner.dindContainer).extraVolumeMounts -}}
  {{- toYaml . | nindent 2 -}}
{{- end }}
{{ if not (empty .Values.runner.dindContainer.resources) }}
resources:
  {{- toYaml .Values.runner.dindContainer.resources | nindent 2 -}}
{{- end }}
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
  {{- range $i, $volume := (.Values.runner.extraVolumes) }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 }}
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
  {{- range $i, $volume := (.Values.runner.extraVolumes) }}
    {{- if eq $volume.name "work" }}
      {{- $createWorkVolume = 0 }}
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

{{- define "gha-runner-scale-set.extra-volumes" -}}
  {{- $filtered := list -}}
  {{- range $i, $volume := (.Values.runner.extraVolumes) }}
    {{- if ne $volume.name "work" -}}
      {{- $filtered = append $filtered $volume -}}
    {{- end -}}
  {{- end -}}
  {{ if not (empty $filtered) }}
  {{- toYaml $filtered -}}
  {{- end }}
{{- end }}

{{- define "gha-runner-scale-set.extra-containers" -}}
  {{- with (.Values.runner.extraContainers) }}
      {{- toYaml . | indent 2 -}}
  {{- end -}}
{{- end }}

{{- define "gha-runner-scale-set.extra-init-containers" -}}
  {{- with (.Values.runner.extraInitContainers) }}
      {{- toYaml . | indent 2 -}}
  {{- end -}}
{{- end }}

{{- define "gha-runner-scale-set.dind-runner-container" -}}
image: {{ .Values.runner.runnerContainer.image }}
imagePullPolicy: {{ .Values.runner.runnerContainer.imagePullPolicy }}
command: ["/home/runner/run.sh"]
{{ if not (empty .Values.runner.runnerContainer.extraArgs) }}
args:
  {{- with .Values.runner.runnerContainer.extraArgs }}
    {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end }}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- $setDockerHost := 1 }}
{{- $setRunnerWaitDocker := 1 }}
{{- $setNodeExtraCaCerts := 0 }}
{{- $setRunnerUpdateCaCerts := 0 }}
{{- if $tlsConfig.runnerMountPath }}
  {{- $setNodeExtraCaCerts = 1 }}
  {{- $setRunnerUpdateCaCerts = 1 }}
{{- end }}
env:
  {{- with .Values.runner.runnerContainer.extraEnv }}
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
    {{- end }}
  {{- toYaml . | nindent 2 -}}
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
  {{- with .Values.runner.runnerContainer.extraVolumeMounts }}
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
    {{- end }}
    {{- toYaml . | nindent 2 -}}
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
{{ if not (empty .Values.runner.runnerContainer.resources) }}
resources:
  {{- toYaml .Values.runner.runnerContainer.resources | nindent 2 -}}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.kubernetes-mode-runner-container" -}}
image: {{ .Values.runner.runnerContainer.image }}
imagePullPolicy: {{ .Values.runner.runnerContainer.imagePullPolicy }}
command: ["/home/runner/run.sh"]
{{ if not (empty .Values.runner.runnerContainer.extraArgs) -}}
args:
  {{- with .Values.runner.runnerContainer.extraArgs -}}
    {{- toYaml . | nindent 2 -}}
  {{- end }}
{{- end }}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
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
  {{- with .Values.runner.runnerContainer.extraEnv }}
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
    {{- end }}
  {{- toYaml . | nindent 2 -}}
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
  {{- with .Values.runner.runnerContainer.extraVolumeMounts }}
    {{- range $i, $volMount := . }}
      {{- if eq $volMount.name "work" }}
        {{- $mountWork = 0 }}
      {{- end }}
      {{- if eq $volMount.name "github-server-tls-cert" }}
        {{- $mountGitHubServerTLS = 0 }}
      {{- end }}
    {{- end }}
    {{- toYaml . | nindent 2 -}}
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
{{ if not (empty .Values.runner.runnerContainer.resources) }}
resources:
  {{- toYaml .Values.runner.runnerContainer.resources | nindent 2 -}}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set.default-mode-runner-containers" -}}
image: {{ .Values.runner.runnerContainer.image }}
imagePullPolicy: {{ .Values.runner.runnerContainer.imagePullPolicy }}
command: ["/home/runner/run.sh"]
{{ if not (empty .Values.runner.runnerContainer.extraArgs) }}
args:
  {{- with .Values.runner.runnerContainer.extraArgs }}
    {{- toYaml . | nindent 2 }}
  {{- end }}
{{- end }}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) }}
{{- $setNodeExtraCaCerts := 0 }}
{{- $setRunnerUpdateCaCerts := 0 }}
{{- if $tlsConfig.runnerMountPath }}
  {{- $setNodeExtraCaCerts = 1 }}
  {{- $setRunnerUpdateCaCerts = 1 }}
{{- end }}
{{- $mountGitHubServerTLS := 0 }}
  {{- if or .Values.runner.runnerContainer.extraEnv $setNodeExtraCaCerts $setRunnerUpdateCaCerts }}
env:
  {{- with .Values.runner.runnerContainer.extraEnv }}
    {{- range $i, $env := . }}
      {{- if eq $env.name "NODE_EXTRA_CA_CERTS" }}
        {{- $setNodeExtraCaCerts = 0 }}
      {{- end }}
      {{- if eq $env.name "RUNNER_UPDATE_CA_CERTS" }}
        {{- $setRunnerUpdateCaCerts = 0 }}
      {{- end }}
    {{- end }}
    {{- toYaml . | nindent 2 -}}
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

  {{- if or .Values.runner.runnerContainer.extraVolumeMounts $mountGitHubServerTLS }}
volumeMounts:
  {{- with .Values.runner.runnerContainer.extraVolumeMounts }}
    {{- range $i, $volMount := . }}
      {{- if eq $volMount.name "github-server-tls-cert" }}
        {{- $mountGitHubServerTLS = 0 }}
      {{- end }}
    {{- end }}
  {{- toYaml . | nindent 2 -}}
  {{- end }}
  {{- if $mountGitHubServerTLS }}
  - name: github-server-tls-cert
    mountPath: {{ clean (print $tlsConfig.runnerMountPath "/" $tlsConfig.certificateFrom.configMapKeyRef.key) }}
    subPath: {{ $tlsConfig.certificateFrom.configMapKeyRef.key }}
  {{- end }}
  {{- end }}
{{ if not (empty .Values.runner.runnerContainer.resources) }}
resources:
  {{- toYaml .Values.runner.runnerContainer.resources | nindent 2 -}}
{{- end}}
{{- end}}

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
    {{- if hasKey $singleNamespaceControllerDeployments .Release.Namespace }}
      {{- $controllerDeployment = get $singleNamespaceControllerDeployments .Release.Namespace }}
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
    {{- if hasKey $singleNamespaceControllerDeployments .Release.Namespace }}
      {{- $controllerDeployment = get $singleNamespaceControllerDeployments .Release.Namespace }}
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
