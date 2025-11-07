{{- define "listener-template.pod" -}}
{{- $metadata := .Values.listenerPodTemplate.metadata | default dict -}}
{{- $spec := .Values.listenerPodTemplate.spec | default dict -}}
{{- if and (empty $metadata) (empty $spec) -}}
  {{- fail "listenerPodTemplate must have at least metadata or spec defined" -}}
{{- end -}}
{{- with $metadata -}}
metadata:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $spec -}}
spec:
  {{- $containers := (index . "containers" | default (list)) -}}
  {{- if empty $containers }}
  containers:
    - name: listener
  {{- else }}
  containers:
    {{- toYaml $containers | nindent 4 }}
  {{- end }}
  {{- $rest := (omit . "containers") -}}
  {{- if gt (len $rest) 0 }}
  {{- toYaml $rest | nindent 2 }}
  {{- end }}
{{- end }}
{{- end -}}