{{/*
Create labels for the runner Pod template (spec.template.metadata.labels).

Order of precedence:
1) resource.all.metadata.labels
2) runner.pod.metadata.labels
3) common labels (cannot be overridden)

Reserved actions.github.com/* labels are excluded from user/global inputs.
*/}}
{{- define "autoscaling-runner-set.runner-pod.labels" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $pod := (index $runner "pod" | default dict) -}}
{{- if not (kindIs "map" $pod) -}}
  {{- fail ".Values.runner.pod must be a map/object" -}}
{{- end -}}
{{- $podMetadata := (index $pod "metadata" | default dict) -}}
{{- if not (kindIs "map" $podMetadata) -}}
  {{- fail ".Values.runner.pod.metadata must be a map/object" -}}
{{- end -}}
{{- $userRaw := (index $podMetadata "labels" | default (dict)) -}}
{{- if not (kindIs "map" $userRaw) -}}
  {{- fail ".Values.runner.pod.metadata.labels must be a map/object" -}}
{{- end -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- $user := include "apply-non-reserved-gha-labels-and-annotations" $userRaw | fromYaml -}}
{{- $common := include "gha-common-labels" . | fromYaml -}}
{{- $labels := mergeOverwrite $global $user $common -}}
{{- if not (empty $labels) -}}
  {{- toYaml $labels -}}
{{- end -}}
{{- end }}

{{/*
Create annotations for the runner Pod template (spec.template.metadata.annotations).

Order of precedence:
1) resource.all.metadata.annotations
2) runner.pod.metadata.annotations

Reserved actions.github.com/* annotations are excluded from user/global inputs.
*/}}
{{- define "autoscaling-runner-set.runner-pod.annotations" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $pod := (index $runner "pod" | default dict) -}}
{{- if not (kindIs "map" $pod) -}}
  {{- fail ".Values.runner.pod must be a map/object" -}}
{{- end -}}
{{- $podMetadata := (index $pod "metadata" | default dict) -}}
{{- if not (kindIs "map" $podMetadata) -}}
  {{- fail ".Values.runner.pod.metadata must be a map/object" -}}
{{- end -}}
{{- $userRaw := (index $podMetadata "annotations" | default (dict)) -}}
{{- if not (kindIs "map" $userRaw) -}}
  {{- fail ".Values.runner.pod.metadata.annotations must be a map/object" -}}
{{- end -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $user := (include "apply-non-reserved-gha-labels-and-annotations" $userRaw) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $user -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations -}}
{{- end -}}
{{- end }}


