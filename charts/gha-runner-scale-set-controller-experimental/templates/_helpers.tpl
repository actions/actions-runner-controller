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

{{- define "gha-controller.selector-labels" -}}
app.kubernetes.io/name: {{ include "gha-controller.name" . }}
app.kubernetes.io/namespace: {{ include "gha-controller.namespace" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gha-controller.labels" -}}
{{- include "gha-controller.labels" . -}}
{{- end }}