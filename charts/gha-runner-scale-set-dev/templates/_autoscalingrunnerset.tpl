{{/*
Create the labels for the autoscaling runner set.
*/}}
{{- define "autoscaling-runner-set.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "autoscaling-runner-set" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.autoscalingRunnerSet.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}

{{/*
Create the annotations for the autoscaling runner set.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.autoscalingRunnerSet.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "autoscaling-runner-set.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.autoscalingRunnerSet.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}

{{/*
Container spec that is expanded for the runner container
*/}}
{{- define "autoscaling-runner-set.template-runner-container" -}}
{{- if not .Values.runner.container }}
  {{ fail "You must provide a runner container specification in values.runner.container" }}
{{- end }}
{{- $tlsConfig := (default (dict) .Values.githubServerTLS) -}}
name: runner
image: {{ .Values.runner.container.image | default "ghcr.io/actions/runner:latest" }}
command: {{ toJson (default (list "/home/runner/run.sh") .Values.runner.container.command) }}
{{- $extra := omit .Values.runner.container "name" "image" "command" -}}
{{- if not (empty $extra) -}}
{{toYaml $extra }}
{{- end -}}
{{- end }}

{{- define "autoscaling-runner-set.template-service-account" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $runnerMode := (index $runner "mode" | default "") -}}
{{- $kubeMode := (index $runner "kubernetesMode" | default dict) -}}
{{- $kubeServiceAccountName := (index $kubeMode "serviceAccountName" | default "") -}}
{{- $kubeDefaults := (index $kubeMode "default" | default true) -}}
{{- if ne $runnerMode "kubernetes" }}
  {{-  include "no-permission-serviceaccount.name" . }}
{{- else if not (empty $kubeServiceAccountName) }}
  {{- $kubeServiceAccountName }}
{{- else if $kubeDefaults }}
  {{- include "kube-mode-serviceaccount.name" . }}
{{- else }}
  {{- fail "runner.kubernetesMode.serviceAccountName must be set when runner.mode is 'kubernetes' and runner.kubernetesMode.default is false" -}}
{{- end }}
{{- end }}
