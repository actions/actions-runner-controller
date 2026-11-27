{{/*
Container spec that is expanded for the runner container
*/}}
{{- define "runner-mode-empty.runner-container" -}}
{{- if not .Values.runner.container }}
  {{ fail "You must provide a runner container specification in values.runner.container" }}
{{- end }}
{{- include "runner-container.render" (dict "root" .) -}}
{{- end }}