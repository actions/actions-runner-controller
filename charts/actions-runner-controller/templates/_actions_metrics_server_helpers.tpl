{{/*
Expand the name of the chart.
*/}}
{{- define "actions-runner-controller-actions-metrics-server.name" -}}
{{- default .Chart.Name .Values.actionsMetricsServer.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "actions-runner-controller-actions-metrics-server.instance" -}}
{{- printf "%s-%s" .Release.Name "actions-metrics-server" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "actions-runner-controller-actions-metrics-server.fullname" -}}
{{- if .Values.actionsMetricsServer.fullnameOverride }}
{{- .Values.actionsMetricsServer.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.actionsMetricsServer.nameOverride }}
{{- $instance := include "actions-runner-controller-actions-metrics-server.instance" . }}
{{- if contains $name $instance }}
{{- $instance | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s-%s" .Release.Name $name "actions-metrics-server" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "actions-runner-controller-actions-metrics-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "actions-runner-controller-actions-metrics-server.name" . }}
app.kubernetes.io/instance: {{ include "actions-runner-controller-actions-metrics-server.instance" . }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "actions-runner-controller-actions-metrics-server.serviceAccountName" -}}
{{- if .Values.actionsMetricsServer.serviceAccount.create }}
{{- default (include "actions-runner-controller-actions-metrics-server.fullname" .) .Values.actionsMetricsServer.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.actionsMetricsServer.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "actions-runner-controller-actions-metrics-server.secretName" -}}
{{- default (include "actions-runner-controller-actions-metrics-server.fullname" .) .Values.actionsMetricsServer.secret.name }}
{{- end }}

{{- define "actions-runner-controller-actions-metrics-server.roleName" -}}
{{- include "actions-runner-controller-actions-metrics-server.fullname" . }}
{{- end }}

{{- define "actions-runner-controller-actions-metrics-server.serviceMonitorName" -}}
{{- include "actions-runner-controller-actions-metrics-server.fullname" . | trunc 47 }}-service-monitor
{{- end }}
