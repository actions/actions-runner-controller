{{- define "secret-resolution.type" -}}
{{- $config := .Values.secretResolution -}}
{{- include "assert-map" (dict "value" $config "path" ".Values.secretResolution") -}}
{{- if not $config -}}
kubernetes
{{- else -}}
  {{- $type := $config.type -}}
  {{- if not (kindIs "string" $type) -}}
    {{- fail (printf "Unsupported keyVault type: %v" $type) -}}
  {{- end -}}
  {{- if not (has $type (list "kubernetes" "azureKeyVault")) -}}
    {{- fail (printf "Unsupported keyVault type: %s" $type) -}}
  {{- end -}}
  {{- $type -}}
{{- end -}}
{{- end -}}
