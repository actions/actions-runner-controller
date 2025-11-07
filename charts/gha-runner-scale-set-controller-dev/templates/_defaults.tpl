{{- define "gha-base-name" -}}
gha-rs-controller
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
{{- define "gha-common.labels" -}}
helm.sh/chart: {{ include "gha-runner-scale-set-controller.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/part-of: "gha-rs-controller"
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
app.kubernetes.io/name: {{ include "gha-runner-scale-set-controller.name" . }}
app.kubernetes.io/namespace: {{ include "gha-runner-scale-set-controller.namespace" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}


{{- define "gha-controller.manager-cluster-role-name" -}}
{{- include "gha-controller.fullname" . }}
{{- end }}

{{- define "gha-controller.manager-cluster-role-binding" -}}
{{- include "gha-controller.fullname" . }}
{{- end }}

{{- define "gha-controller.managerSingleNamespaceRoleName" -}}
{{- include "gha-controller.manager-single-namespace-role-name" . -}}
{{- end }}

{{- define "gha-controller.managerSingleNamespaceRoleBinding" -}}
{{- include "gha-controller.manager-single-namespace-role-binding" . -}}
{{- end }}

{{- define "gha-controller.managerSingleNamespaceWatchRoleName" -}}
{{- include "gha-controller.manager-single-namespace-watch-role-name" . -}}
{{- end }}

{{- define "gha-controller.managerSingleNamespaceWatchRoleBinding" -}}
{{- include "gha-controller.manager-single-namespace-watch-role-binding" . -}}
{{- end }}

{{- define "gha-controller.managerListenerRoleName" -}}
{{- include "gha-controller.manager-listener-role-name" . -}}
{{- end }}

{{- define "gha-controller.managerListenerRoleBinding" -}}
{{- include "gha-controller.manager-listener-role-binding" . -}}
{{- end }}

{{- define "gha-controller.manager-single-namespace-role-name" -}}
{{- include "gha-controller.fullname" . }}-single-namespace
{{- end }}

{{- define "gha-controller.manager-single-namespace-role-binding" -}}
{{- include "gha-controller.fullname" . }}-single-namespace
{{- end }}

{{- define "gha-controller.manager-single-namespace-watch-role-name" -}}
{{- include "gha-controller.fullname" . }}-single-namespace-watch
{{- end }}

{{- define "gha-controller.manager-single-namespace-watch-role-binding" -}}
{{- include "gha-controller.fullname" . }}-single-namespace-watch
{{- end }}

{{- define "gha-controller.manager-listener-role-name" -}}
{{- include "gha-controller.fullname" . }}-listener
{{- end }}

{{- define "gha-controller.manager-listener-role-binding" -}}
{{- include "gha-controller.fullname" . }}-listener
{{- end }}

{{- define "gha-controller.leaderElectionRoleName" -}}
{{- include "gha-controller.fullname" . }}-leader-election
{{- end }}

{{- define "gha-controller.leader-election-role-name" -}}
{{- include "gha-controller.leaderElectionRoleName" . -}}
{{- end }}

{{- define "gha-controller.leaderElectionRoleBinding" -}}
{{- include "gha-controller.fullname" . }}-leader-election
{{- end }}

{{- define "gha-controller.leader-election-role-binding" -}}
{{- include "gha-controller.leaderElectionRoleBinding" . -}}
{{- end }}