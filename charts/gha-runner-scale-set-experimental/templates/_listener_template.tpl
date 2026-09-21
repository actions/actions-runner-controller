{{- define "listener-template.pod" -}}
{{- $metadata := .metadata | default dict -}}
{{- $spec := .spec | default dict -}}
{{- if and (empty $metadata) (empty $spec) -}}
  {{- fail ".Values.listener.podTemplate must have at least metadata or spec defined" -}}
{{- end -}}
{{- with $metadata -}}
{{- $out := omit . "labels" "annotations" -}}
{{- with .labels -}}
{{- $_ := set $out "labels" (fromYaml (include "string-map" .)) -}}
{{- end -}}
{{- with .annotations -}}
{{- $_ := set $out "annotations" (fromYaml (include "string-map" .)) -}}
{{- end -}}
metadata:
  {{- toYaml $out | nindent 2 }}
{{ end }}
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