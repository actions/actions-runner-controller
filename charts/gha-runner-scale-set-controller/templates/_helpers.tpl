{{/*
Expand the name of the chart.
*/}}


{{- define "gha-base-name" -}}
gha-rs-controller
{{- end }}

{{/*
Allow overriding the namespace for the resources.
*/}}
{{- define "gha-runner-scale-set-controller.namespace" -}}
{{- if .Values.namespaceOverride }}
  {{- .Values.namespaceOverride }}
{{- else }}
  {{- .Release.Namespace }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set-controller.name" -}}
{{- default (include "gha-base-name" .) .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gha-runner-scale-set-controller.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default (include "gha-base-name" .) .Values.nameOverride }}
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
{{- define "gha-runner-scale-set-controller.chart" -}}
{{- printf "%s-%s" (include "gha-base-name" .) .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gha-runner-scale-set-controller.labels" -}}
helm.sh/chart: {{ include "gha-runner-scale-set-controller.chart" . }}
{{ include "gha-runner-scale-set-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: gha-rs-controller
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- range $k, $v := .Values.labels }}
{{ $k }}: {{ $v | quote }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "gha-runner-scale-set-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gha-runner-scale-set-controller.name" . }}
app.kubernetes.io/namespace: {{ include "gha-runner-scale-set-controller.namespace" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "gha-runner-scale-set-controller.serviceAccountName" -}}
{{- if eq .Values.serviceAccount.name "default"}}
  {{- fail "serviceAccount.name cannot be set to 'default'" }}
{{- end }}
{{- if .Values.serviceAccount.create }}
  {{- default (include "gha-runner-scale-set-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
  {{- if not .Values.serviceAccount.name }}
    {{- fail "serviceAccount.name must be set if serviceAccount.create is false" }}
  {{- else }}
    {{- .Values.serviceAccount.name }}
  {{- end }}
{{- end }}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerClusterRoleName" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerClusterRoleBinding" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceRoleName" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-single-namespace
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceRoleBinding" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-single-namespace
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceWatchRoleName" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-single-namespace-watch
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceWatchRoleBinding" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-single-namespace-watch
{{- end }}

{{- define "gha-runner-scale-set-controller.managerListenerRoleName" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-listener
{{- end }}

{{- define "gha-runner-scale-set-controller.managerListenerRoleBinding" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-listener
{{- end }}

{{- define "gha-runner-scale-set-controller.leaderElectionRoleName" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-leader-election
{{- end }}

{{- define "gha-runner-scale-set-controller.leaderElectionRoleBinding" -}}
{{- include "gha-runner-scale-set-controller.fullname" . }}-leader-election
{{- end }}

{{- define "gha-runner-scale-set-controller.imagePullSecretsNames" -}}
{{- $names := list }}
{{- range $k, $v := . }}
  {{- $names = append $names $v.name }}
{{- end }}
{{- $names | join ","}}
{{- end }}
