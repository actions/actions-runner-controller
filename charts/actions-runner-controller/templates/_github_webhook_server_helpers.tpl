{{/*
Expand the name of the chart.
*/}}
{{- define "actions-runner-controller-github-webhook-server.name" -}}
{{- default .Chart.Name .Values.githubWebhookServer.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "actions-runner-controller-github-webhook-server.instance" -}}
{{- printf "%s-%s" .Release.Name "github-webhook-server" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "actions-runner-controller-github-webhook-server.fullname" -}}
{{- if .Values.githubWebhookServer.fullnameOverride }}
{{- .Values.githubWebhookServer.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.githubWebhookServer.nameOverride }}
{{- $instance := include "actions-runner-controller-github-webhook-server.instance" . }}
{{- if contains $name $instance }}
{{- $instance | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s-%s" .Release.Name $name "github-webhook-server" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "actions-runner-controller-github-webhook-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "actions-runner-controller-github-webhook-server.name" . }}
app.kubernetes.io/instance: {{ include "actions-runner-controller-github-webhook-server.instance" . }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "actions-runner-controller-github-webhook-server.serviceAccountName" -}}
{{- if .Values.githubWebhookServer.serviceAccount.create }}
{{- default (include "actions-runner-controller-github-webhook-server.fullname" .) .Values.githubWebhookServer.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.githubWebhookServer.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "actions-runner-controller-github-webhook-server.secretName" -}}
{{- default (include "actions-runner-controller-github-webhook-server.fullname" .) .Values.githubWebhookServer.secret.name }}
{{- end }}

{{- define "actions-runner-controller-github-webhook-server.roleName" -}}
{{- include "actions-runner-controller-github-webhook-server.fullname" . }}
{{- end }}

{{- define "actions-runner-controller-github-webhook-server.serviceMonitorName" -}}
{{- include "actions-runner-controller-github-webhook-server.fullname" . | trunc 47 }}-service-monitor
{{- end }}

{{- define "actions-runner-controller-github-webhook-server.pdbName" -}}
{{- include "actions-runner-controller-github-webhook-server.fullname" . | trunc 59 }}-pdb
{{- end }}