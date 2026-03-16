{{/*
Container spec that is expanded for the runner container
*/}}
{{- define "runner-mode-empty.runner-container" -}}
{{- if not .Values.runner.container }}
  {{ fail "You must provide a runner container specification in values.runner.container" }}
{{- end }}
name: runner
image: {{ .Values.runner.container.image | default "ghcr.io/actions/actions-runner:latest" }}
command: {{ toJson (default (list "/home/runner/run.sh") .Values.runner.container.command) }}

{{ $tlsEnvItems := include "githubServerTLS.envItems" (dict "root" $ "existingEnv" (.Values.runner.container.env | default list)) }}
{{ if or .Values.runner.container.env $tlsEnvItems }}
env:
  {{- with .Values.runner.container.env }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{ $tlsEnvItems | nindent 2 }}
{{ end }}

{{ $tlsVolumeMountItem := include "githubServerTLS.volumeMountItem" (dict "root" $ "existingVolumeMounts" (.Values.runner.container.volumeMounts | default list)) }}
{{ if or .Values.runner.container.volumeMounts $tlsVolumeMountItem }}
volumeMounts:
  {{- with .Values.runner.container.volumeMounts }}
  {{- toYaml . | nindent 2 }}
  {{- end }}
{{ $tlsVolumeMountItem | nindent 2 }}
{{ end }}

{{ $extra := omit .Values.runner.container "name" "image" "command" "env" "volumeMounts" }}
{{- if not (empty $extra) -}}
{{ toYaml $extra }}
{{- end -}}
{{- end }}