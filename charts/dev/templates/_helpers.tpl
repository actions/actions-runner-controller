{{- define "autoscaling-runner-set.name" -}}
{{- $name := .Values.runnerScaleSetName | default .Release.Name | replace "_" "-" | trimSuffix "-" }}
{{- if or (empty $name) (gt (len $name) 45) }}
{{ fail "Autoscaling runner set name must have up to 45 characters" }}
{{- end }}
{{- $name }}
{{- end }}

{{- define "autoscaling-runner-set.namespace" -}}
{{- .Values.namespaceOverride | default .Release.Namespace -}}
{{- end }}


{{- define "githubsecret.name" -}}
{{- if not (empty .Values.auth.secretName) }}
{{- quote .Values.auth.secretName }}
{{- else }}
{{- include "autoscaling-runner-set.name" . }}-github-secret
{{- end }}
{{- end }}
