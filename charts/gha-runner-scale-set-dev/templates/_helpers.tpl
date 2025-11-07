{{/*
Create the labels for the GitHub auth secret.
*/}}
{{- define "github-secret.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "github-secret" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $resourceLabels $commonLabels) }}
{{- end }}


{{/*
Create the annotations for the GitHub auth secret.

Only global annotations are applied.
Reserved annotations are excluded.
*/}}
{{- define "github-secret.annotations" -}}
{{- $annotations := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}

{{/*
Create the labels for the no-permission ServiceAccount.
*/}}
{{- define "no-permission-serviceaccount.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "no-permission-serviceaccount" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.noPermissionServiceAccount.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}


{{/*
Create the annotations for the no-permission ServiceAccount.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.noPermissionServiceAccount.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "no-permission-serviceaccount.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.noPermissionServiceAccount.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}


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


