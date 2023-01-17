{{/*
Expand the name of the chart.
*/}}
{{- define "actions-runner-controller-2.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "actions-runner-controller-2.fullname" -}}
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
{{- define "actions-runner-controller-2.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "actions-runner-controller-2.labels" -}}
helm.sh/chart: {{ include "actions-runner-controller-2.chart" . }}
{{ include "actions-runner-controller-2.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- range $k, $v := .Values.labels }}
{{ $k }}: {{ $v }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "actions-runner-controller-2.selectorLabels" -}}
app.kubernetes.io/name: {{ include "actions-runner-controller-2.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "actions-runner-controller-2.serviceAccountName" -}}
{{- if eq .Values.serviceAccount.name "default"}}
{{- fail "serviceAccount.name cannot be set to 'default'" }}
{{- end }}
{{- if .Values.serviceAccount.create }}
{{- default (include "actions-runner-controller-2.fullname" .) .Values.serviceAccount.name }}
{{- else }}
    {{- if not .Values.serviceAccount.name }}
{{- fail "serviceAccount.name must be set if serviceAccount.create is false" }}
    {{- else }}
{{- .Values.serviceAccount.name }}
    {{- end }}
{{- end }}
{{- end }}

{{- define "actions-runner-controller-2.managerRoleName" -}}
{{- include "actions-runner-controller-2.fullname" . }}-manager-role
{{- end }}

{{- define "actions-runner-controller-2.managerRoleBinding" -}}
{{- include "actions-runner-controller-2.fullname" . }}-manager-rolebinding
{{- end }}

{{- define "actions-runner-controller-2.leaderElectionRoleName" -}}
{{- include "actions-runner-controller-2.fullname" . }}-leader-election-role
{{- end }}

{{- define "actions-runner-controller-2.leaderElectionRoleBinding" -}}
{{- include "actions-runner-controller-2.fullname" . }}-leader-election-rolebinding
{{- end }}

{{- define "actions-runner-controller-2.imagePullSecretsNames" -}}
{{- $names := list }}
{{- range $k, $v := . }}
{{- $names = append $names $v.name }}
{{- end }}
{{- $names | join ","}}
{{- end }}