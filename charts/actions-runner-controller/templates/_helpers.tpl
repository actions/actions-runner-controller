{{/*
Expand the name of the chart.
*/}}
{{- define "actions-runner-controller.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "actions-runner-controller.fullname" -}}
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
{{- define "actions-runner-controller.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "actions-runner-controller.labels" -}}
helm.sh/chart: {{ include "actions-runner-controller.chart" . }}
{{ include "actions-runner-controller.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- range $k, $v := .Values.labels }}
{{ $k }}: {{ $v }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "actions-runner-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "actions-runner-controller.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "actions-runner-controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "actions-runner-controller.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "actions-runner-controller.secretName" -}}
{{- default (include "actions-runner-controller.fullname" .) .Values.authSecret.name -}}
{{- end }}

{{- define "actions-runner-controller.githubWebhookServerSecretName" -}}
{{- default (include "actions-runner-controller.fullname" .) .Values.githubWebhookServer.secret.name -}}
{{- end }}

{{- define "actions-runner-controller.leaderElectionRoleName" -}}
{{- include "actions-runner-controller.fullname" . }}-leader-election
{{- end }}

{{- define "actions-runner-controller.authProxyRoleName" -}}
{{- include "actions-runner-controller.fullname" . }}-proxy
{{- end }}

{{- define "actions-runner-controller.managerRoleName" -}}
{{- include "actions-runner-controller.fullname" . }}-manager
{{- end }}

{{- define "actions-runner-controller.runnerEditorRoleName" -}}
{{- include "actions-runner-controller.fullname" . }}-runner-editor
{{- end }}

{{- define "actions-runner-controller.runnerViewerRoleName" -}}
{{- include "actions-runner-controller.fullname" . }}-runner-viewer
{{- end }}

{{- define "actions-runner-controller.webhookServiceName" -}}
{{- include "actions-runner-controller.fullname" . | trunc 55 }}-webhook
{{- end }}

{{- define "actions-runner-controller.metricsServiceName" -}}
{{- include "actions-runner-controller.fullname" . | trunc 47 }}-metrics-service
{{- end }}

{{- define "actions-runner-controller.serviceMonitorName" -}}
{{- include "actions-runner-controller.fullname" . | trunc 47 }}-service-monitor
{{- end }}

{{- define "actions-runner-controller.selfsignedIssuerName" -}}
{{- include "actions-runner-controller.fullname" . }}-selfsigned-issuer
{{- end }}

{{- define "actions-runner-controller.servingCertName" -}}
{{- include "actions-runner-controller.fullname" . }}-serving-cert
{{- end }}

{{- define "actions-runner-controller.pdbName" -}}
{{- include "actions-runner-controller.fullname" . | trunc 59 }}-pdb
{{- end }}
