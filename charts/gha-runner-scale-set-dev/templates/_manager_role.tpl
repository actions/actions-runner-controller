{{/*
Create the labels for the manager Role.
*/}}
{{- define "manager-role.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "manager-role" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.managerRole.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}


{{/*
Create the annotations for the manager Role.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.managerRole.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "manager-role.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.managerRole.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}


{{/*
The name of the manager RoleBinding.

Kept intentionally aligned with the manager Role name, mirroring the legacy
chart behavior.
*/}}
{{- define "manager-role-binding.name" -}}
{{- include "manager-role.name" . -}}
{{- end }}


{{/*
Create the labels for the manager RoleBinding.
*/}}
{{- define "manager-role-binding.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "manager-role-binding" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.managerRoleBinding.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}


{{/*
Create the annotations for the manager RoleBinding.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.managerRoleBinding.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "manager-role-binding.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.managerRoleBinding.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}