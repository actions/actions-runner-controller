{{/*
Takes a map of user labels and removes the ones with "actions.github.com/" prefix
*/}}
{{- define "apply-non-reserved-gha-labels-and-annotations" -}}
{{- $userLabels := . -}}
{{- $processed := dict -}}
{{- range $key, $value := $userLabels -}}
  {{- if not (hasPrefix "actions.github.com/" $key) -}}
    {{- $_ := set $processed $key $value -}}
  {{- end -}}
{{- end -}}
{{- if not (empty $processed) -}}
  {{- $processed | toYaml }}
{{- end }}
{{- end }}

{{/*
Compatibility helpers: this dev chart historically reused template names from the
non-dev controller chart. Define aliases so rendering and helm-unittest work.
*/}}

{{- define "gha-controller.fullname" -}}
{{- include "gha-controller.name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.namespace" -}}
{{- include "gha-controller.namespace" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.name" -}}
{{- include "gha-controller.name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.fullname" -}}
{{- include "gha-controller.fullname" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gha-runner-scale-set-controller.name" . }}
app.kubernetes.io/namespace: {{ include "gha-runner-scale-set-controller.namespace" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gha-runner-scale-set-controller.labels" -}}
{{- include "gha-controller.labels" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.serviceAccountName" -}}
{{- include "gha-controller.service-account-name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerClusterRoleName" -}}
{{- include "gha-controller.manager-cluster-role-name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerClusterRoleBinding" -}}
{{- include "gha-controller.manager-cluster-role-binding" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceRoleName" -}}
{{- include "gha-controller.manager-single-namespace-role-name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceRoleBinding" -}}
{{- include "gha-controller.manager-single-namespace-role-binding" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceWatchRoleName" -}}
{{- include "gha-controller.manager-single-namespace-watch-role-name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerSingleNamespaceWatchRoleBinding" -}}
{{- include "gha-controller.manager-single-namespace-watch-role-binding" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerListenerRoleName" -}}
{{- include "gha-controller.manager-listener-role-name" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.managerListenerRoleBinding" -}}
{{- include "gha-controller.manager-listener-role-binding" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.leaderElectionRoleName" -}}
{{- include "gha-controller.leaderElectionRoleName" . -}}
{{- end }}

{{- define "gha-runner-scale-set-controller.leaderElectionRoleBinding" -}}
{{- include "gha-controller.leaderElectionRoleBinding" . -}}
{{- end }}