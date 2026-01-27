{{- define "gha-base-name" -}}
gha-rs-controller
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gha-controller.chart" -}}
{{- printf "%s-%s" (include "gha-base-name" .) .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "gha-common.labels" -}}
helm.sh/chart: {{ include "gha-controller.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: "gha-rs-controller"
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
app.kubernetes.io/name: {{ include "gha-controller.name" . }}
app.kubernetes.io/namespace: {{ include "gha-controller.namespace" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}


{{- define "gha-controller.manager-cluster-role-name" -}}
{{- include "gha-controller.name" . }}
{{- end }}

{{- define "gha-controller.manager-cluster-role-binding" -}}
{{- include "gha-controller.name" . }}
{{- end }}

{{- define "gha-controller.manager-single-namespace-role-name" -}}
{{- include "gha-controller.name" . }}-single-namespace
{{- end }}

{{- define "gha-controller.manager-single-namespace-role-binding" -}}
{{- include "gha-controller.name" . }}-single-namespace
{{- end }}

{{- define "gha-controller.manager-single-namespace-watch-role-name" -}}
{{- include "gha-controller.name" . }}-single-namespace-watch
{{- end }}

{{- define "gha-controller.manager-single-namespace-watch-role-binding" -}}
{{- include "gha-controller.name" . }}-single-namespace-watch
{{- end }}

{{- define "gha-controller.manager-listener-role-name" -}}
{{- include "gha-controller.name" . }}-listener
{{- end }}

{{- define "gha-controller.manager-listener-role-binding" -}}
{{- include "gha-controller.name" . }}-listener
{{- end }}

{{- define "gha-controller.leaderElectionRoleName" -}}
{{- include "gha-controller.name" . }}-leader-election
{{- end }}

{{- define "gha-controller.leader-election-role-name" -}}
{{- include "gha-controller.leaderElectionRoleName" . -}}
{{- end }}

{{- define "gha-controller.leaderElectionRoleBinding" -}}
{{- include "gha-controller.name" . }}-leader-election
{{- end }}

{{- define "gha-controller.leader-election-role-binding" -}}
{{- include "gha-controller.leaderElectionRoleBinding" . -}}
{{- end }}