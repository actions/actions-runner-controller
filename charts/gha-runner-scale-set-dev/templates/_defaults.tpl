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
The name of the manager Role.
*/}}
{{- define "manager-role.name" -}}
{{- printf "%s-manager-role" (include "autoscaling-runner-set.name" .) -}}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "gha-runner-scale-set.chart" -}}
{{- printf "gha-rs-%s" .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
The name of the GitHub secret used for authentication.
*/}}
{{- define "github-secret.name" -}}
{{- if not (empty .Values.auth.secretName) -}}
  {{- .Values.auth.secretName -}}
{{- else -}}
  {{- include "autoscaling-runner-set.name" . }}-github-secret
{{- end -}}
{{- end }}

{{/*
The name of the no-permission ServiceAccount.

This ServiceAccount is intended for non-kubernetes runner modes when the user
has not specified an explicit ServiceAccount.
*/}}
{{- define "no-permission-serviceaccount.name" -}}
{{- printf "%s-no-permission" (include "autoscaling-runner-set.name" .) -}}
{{- end }}

{{/*
The name of the kubernetes-mode Role.

Kept intentionally aligned with the legacy chart behavior.
*/}}
{{- define "kube-mode-role.name" -}}
{{- printf "%s-kube-mode" (include "autoscaling-runner-set.name" .) -}}
{{- end }}


{{/*
The name of the kubernetes-mode RoleBinding.

Kept intentionally aligned with the kubernetes-mode Role name.
*/}}
{{- define "kube-mode-role-binding.name" -}}
{{- include "kube-mode-role.name" . -}}
{{- end }}


{{/*
The name of the kubernetes-mode ServiceAccount.

Kept intentionally aligned with the legacy chart behavior.
*/}}
{{- define "kube-mode-serviceaccount.name" -}}
{{- include "kube-mode-role.name" . -}}
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
Get the runner container image.
It defaults to ghcr.io/actions/actions-runner:latest if not specified.
*/}}
{{- define "runner.image" -}}
{{- $runner := .Values.runner.container | default dict -}}
{{- if not (kindIs "map" $runner) -}}
  {{- fail "runner.container must be a map/object" -}}
{{- end -}}
{{- $image := $runner.image | default "ghcr.io/actions/actions-runner:latest" -}}
{{- if not (kindIs "string" $image) -}}
  {{- fail "runner.container.image must be a string" -}}
{{- end -}}
{{- $image }}
{{- end }}

{{- define "runner.command" -}}
{{- $runner := .Values.runner.container | default dict -}}
{{- if not (kindIs "map" $runner) -}}
  {{- fail "runner.container must be a map/object" -}}
{{- end -}}
{{- $command := $runner.command | default (list "/home/runner/run.sh") -}}
{{- if not (kindIs "slice" $command) -}}
  {{- fail "runner.container.command must be a list/array" -}}
{{- end -}}
{{- toJson $command -}}
{{- end }}