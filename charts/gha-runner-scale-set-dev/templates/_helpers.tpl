
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

{{/*
The name of the GitHub secret used for authentication.
*/}}
{{- define "github-secret.name" -}}
{{- if not (empty .Values.auth.secretName) }}
{{- quote .Values.auth.secretName }}
{{- else }}
{{- include "autoscaling-runner-set.name" . }}-github-secret
{{- end }}
{{- end }}


{{/*
Create the labels for the autoscaling runner set.
*/}}
{{- define "autoscaling-runner-set.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "autoscaling-runner-set" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "gha-process-labels" (.Values.resource.autoscalingRunnerSet.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "gha-process-labels" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}

{{/*
Create the common labels used across all resources.
*/}}
{{- define "gha-common-labels" -}}
helm.sh/chart: {{ include "gha-runner-scale-set.chart" . }}
app.kubernetes.io/name: {{ include "autoscaling-runner-set.name" . }}
app.kubernetes.io/instance: {{ include "autoscaling-runner-set.name" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: "gha-rs"
actions.github.com/scale-set-name: {{ include "autoscaling-runner-set.name" . }}
actions.github.com/scale-set-namespace: {{ include "autoscaling-runner-set.namespace" . }}
{{- end }}


{{/*
Takes a map of user labels and removes the ones with "actions.github.com/" prefix
*/}}
{{- define "gha-process-labels" -}}
{{- $userLabels := . -}}
{{- $processed := dict -}}
{{- range $key, $value := $userLabels -}}
  {{- if not (hasPrefix $key "actions.github.com/") -}}
    {{- $_ := set $processed $key $value -}}
  {{- end -}}
{{- end -}}
{{- $processed | toYaml -}}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gha-runner-scale-set.chart" -}}
{{- printf "gha-rs-%s" .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Container spec that is expanded for the runner container
*/}}
{{- define "container-spec.runner" -}}

{{- if not .Values.runner.container }}
{{ fail "You must provide a runner container specification in values.runner.container" }}
{{- end }}

{{- $tlsConfig := (default (dict) .Values.githubServerTLS) -}}
name: runner
image: {{ .Values.runner.container.image | default "ghcr.io/actions/runner:latest" }}
command: {{ toJson (default (list "/home/runner/run.sh") .Values.runner.container.command) }}
{{- end }}

