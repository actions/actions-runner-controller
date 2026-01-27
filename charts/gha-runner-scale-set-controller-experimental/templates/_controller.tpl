{{/*
Allow overriding the namespace for the resources.
*/}}
{{- define "gha-controller.namespace" -}}
{{- if .Values.namespaceOverride }}
  {{- .Values.namespaceOverride }}
{{- else }}
  {{- .Release.Namespace }}
{{- end }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "gha-controller.name" -}}
{{- if .Values.nameOverride }}
  {{- .Values.nameOverride | trunc 63 | trimSuffix "-" }}
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
Labels applied to the controller deployment
*/}}
{{- define "gha-controller.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "controller-manager" -}}
{{- $commonLabels := include "gha-common.labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.controller.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.labels | default (dict)) | fromYaml -}}
{{- $labels := mergeOverwrite $global $userLabels $resourceLabels $commonLabels -}}

{{- /* Reserved actions.github.com/* labels owned by the chart itself */ -}}
{{- $_ := set $labels "actions.github.com/controller-service-account-namespace" (include "gha-controller.namespace" .) -}}
{{- $_ := set $labels "actions.github.com/controller-service-account-name" (include "gha-controller.service-account-name" .) -}}
{{- with .Values.controller.flags.watchSingleNamespace }}
  {{- $_ := set $labels "actions.github.com/controller-watch-single-namespace" . -}}
{{- end }}

{{- toYaml $labels -}}
{{- end }}


{{/*
Create the name of the service account to use
*/}}
{{- define "gha-controller.service-account-name" -}}
{{- if eq .Values.controller.serviceAccount.name "default"}}
  {{- fail "serviceAccount.name cannot be set to 'default'" }}
{{- end }}
{{- if .Values.controller.serviceAccount.create }}
  {{- default (include "gha-controller.name" .) .Values.controller.serviceAccount.name }}
{{- else }}
  {{- if not .Values.controller.serviceAccount.name }}
    {{- fail "serviceAccount.name must be set if serviceAccount.create is false" }}
  {{- else }}
    {{- .Values.controller.serviceAccount.name }}
  {{- end }}
{{- end }}
{{- end }}