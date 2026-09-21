{{/*
Render a single label or annotation value as a string.
Values from a values file arrive as float64, so "%v" would turn large integers into
scientific notation (12345678901234 -> 1.2345678901234e+13) and silently write a value the
user never asked for. Integral floats are therefore formatted without an exponent.
*/}}
{{- define "metadata-value" -}}
{{- if eq . nil -}}
{{- "" -}}
{{- else if and (kindIs "float64" .) (eq . (floor .)) -}}
{{- printf "%.0f" . -}}
{{- else -}}
{{- printf "%v" . -}}
{{- end -}}
{{- end }}

{{/*
Render a labels or annotations map with all values coerced to strings.
Kubernetes only accepts string values, so scalars such as `true` or `1` must not be
rendered as YAML booleans or numbers.
*/}}
{{- define "string-map" -}}
{{- $out := dict -}}
{{- range $k, $v := . -}}
{{- $_ := set $out $k (include "metadata-value" $v) -}}
{{- end -}}
{{- toYaml $out -}}
{{- end }}

{{/*
Fail unless a value is absent or a mapping. Used to turn mis-typed metadata values into an
error that names the values path, instead of an opaque "range can't iterate over" further
down the render.
Expects a dict with "value" and "path".
*/}}
{{- define "assert-map" -}}
{{- $value := .value -}}
{{- if and (not (kindIs "invalid" $value)) (not (kindIs "map" $value)) -}}
{{- fail (printf "%s: must be a mapping, got %s" .path (kindOf $value)) -}}
{{- end -}}
{{- end }}

{{/*
Fail unless a metadata value is a scalar. A map or list would otherwise be flattened by the
string coercion into Go's own formatting (`map[a:b]`), which is a syntactically valid but
meaningless label or annotation, and a null would become "<nil>".
Expects a dict with "value", "key", "kind" and "path".
*/}}
{{- define "assert-scalar" -}}
{{- $value := .value -}}
{{- if or (kindIs "map" $value) (kindIs "slice" $value) (kindIs "invalid" $value) -}}
{{- fail (printf "%s: invalid value for %s %q: must be a scalar, got %s. Quote the value if it is meant to be a string" .path .kind .key (kindOf $value)) -}}
{{- end -}}
{{- end }}

{{/*
Validate a label or annotation key against the Kubernetes qualified name rules.
Expects a dict with "key", "kind" (label|annotation) and "path" (the values path used in the error message).
*/}}
{{- define "validate-metadata-key" -}}
{{- $key := .key -}}
{{- $kind := .kind -}}
{{- $path := .path -}}
{{- $parts := splitList "/" $key -}}
{{- $name := $key -}}
{{- if gt (len $parts) 2 -}}
{{- fail (printf "%s: invalid %s key %q: a qualified name must consist of an optional DNS subdomain prefix followed by a single '/'" $path $kind $key) -}}
{{- end -}}
{{- if eq (len $parts) 2 -}}
{{- $prefix := index $parts 0 -}}
{{- $name = index $parts 1 -}}
{{- if gt (len $prefix) 253 -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain of no more than 253 characters" $path $kind $key $prefix) -}}
{{- end -}}
{{- if not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?([.][a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" $prefix) -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain, so it must consist of dot-separated segments of lowercase alphanumeric characters or '-', each starting and ending with an alphanumeric character" $path $kind $key $prefix) -}}
{{- end -}}
{{- end -}}
{{- if or (eq $name "") (gt (len $name) 63) (not (regexMatch "^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$" $name)) -}}
{{- fail (printf "%s: invalid %s key %q: the name part must be no more than 63 characters, consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" $path $kind $key) -}}
{{- end -}}
{{- end }}

{{/*
Validate a metadata block (dict with optional "labels" and "annotations").
Invalid runner pod labels are only rejected once the controller creates the runner pod,
which leaves the scale set without runners, so fail at render time instead.
Expects a dict with "metadata" and "path".
*/}}
{{- define "validate-metadata" -}}
{{- $path := .path -}}
{{- include "assert-map" (dict "value" .metadata "path" $path) -}}
{{- $metadata := .metadata | default dict -}}
{{- $labels := index $metadata "labels" -}}
{{- include "assert-map" (dict "value" $labels "path" (printf "%s.labels" $path)) -}}
{{- range $key, $value := ($labels | default dict) -}}
{{- include "validate-metadata-key" (dict "key" $key "kind" "label" "path" (printf "%s.labels" $path)) -}}
{{- include "assert-scalar" (dict "value" $value "key" $key "kind" "label" "path" (printf "%s.labels" $path)) -}}
{{- $rendered := include "metadata-value" $value -}}
{{- if gt (len $rendered) 63 -}}
{{- fail (printf "%s.labels: invalid value %q for label %q: a label value must be no more than 63 characters" $path $rendered $key) -}}
{{- end -}}
{{- if not (regexMatch "^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$" $rendered) -}}
{{- fail (printf "%s.labels: invalid value %q for label %q: a valid label value must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" $path $rendered $key) -}}
{{- end -}}
{{- end -}}
{{- $annotations := index $metadata "annotations" -}}
{{- include "assert-map" (dict "value" $annotations "path" (printf "%s.annotations" $path)) -}}
{{- range $key, $value := ($annotations | default dict) -}}
{{- include "validate-metadata-key" (dict "key" $key "kind" "annotation" "path" (printf "%s.annotations" $path)) -}}
{{- include "assert-scalar" (dict "value" $value "key" $key "kind" "annotation" "path" (printf "%s.annotations" $path)) -}}
{{- end -}}
{{- end }}

{{/*
Validate every label and annotation map the chart can render onto resources it manages.
*/}}
{{- define "validate-all-metadata" -}}
{{- include "assert-map" (dict "value" .Values.resource "path" ".Values.resource") -}}
{{- range $resource, $config := (.Values.resource | default dict) }}
{{- include "assert-map" (dict "value" $config "path" (printf ".Values.resource.%s" $resource)) -}}
{{- include "validate-metadata" (dict "metadata" (index ($config | default dict) "metadata") "path" (printf ".Values.resource.%s.metadata" $resource)) -}}
{{- end }}
{{- include "assert-map" (dict "value" .Values.runner "path" ".Values.runner") -}}
{{- $runnerPod := index (.Values.runner | default dict) "pod" -}}
{{- include "assert-map" (dict "value" $runnerPod "path" ".Values.runner.pod") -}}
{{- include "validate-metadata" (dict "metadata" (index ($runnerPod | default dict) "metadata") "path" ".Values.runner.pod.metadata") -}}
{{- $listener := .Values.listener | default dict -}}
{{- if kindIs "map" $listener -}}
{{- $listenerPod := index $listener "podTemplate" -}}
{{- if kindIs "map" ($listenerPod | default dict) -}}
{{- include "validate-metadata" (dict "metadata" (index ($listenerPod | default dict) "metadata") "path" ".Values.listener.podTemplate.metadata") -}}
{{- end -}}
{{- end -}}
{{- end }}

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
Takes a map of user labels and removes the ones with "actions.github.com/" prefix.
Values are rendered as strings so that scalars such as `true` or `1.0` do not become
non-string YAML values, which Kubernetes rejects for labels and annotations.
*/}}
{{- define "apply-non-reserved-gha-labels-and-annotations" -}}
{{- $userLabels := . -}}
{{- $processed := dict -}}
{{- range $key, $value := $userLabels -}}
  {{- if not (hasPrefix "actions.github.com/" $key) -}}
    {{- $_ := set $processed $key (include "metadata-value" $value) -}}
  {{- end -}}
{{- end -}}
{{- if not (empty $processed) -}}
  {{- $processed | toYaml }}
{{- end }}
{{- end }}


{{/*
GitHub Server TLS helper parts

These helpers centralize TLS env/volumeMount/volume snippets so that runner modes
inject the certificate consistently.

Behavior:
- If githubServerTLS.runnerMountPath is empty: emit nothing.
- If runnerMountPath is set: require certificateFrom.configMapKeyRef.name + key.
- Avoid duplicating user-provided env vars / volumeMounts.
*/}}

{{- define "githubServerTLS.config" -}}
{{- $tls := (default (dict) .Values.githubServerTLS) -}}
{{- if and (not (empty $tls)) (not (kindIs "map" $tls)) -}}
  {{- fail "githubServerTLS must be a map/object" -}}
{{- end -}}
{{- toYaml $tls -}}
{{- end -}}

{{- define "githubServerTLS.mountPath" -}}
{{- $tls := (include "githubServerTLS.config" .) | fromYaml -}}
{{- (index $tls "runnerMountPath" | default "") -}}
{{- end -}}

{{- define "githubServerTLS.configMapName" -}}
{{- $mountPath := include "githubServerTLS.mountPath" . -}}
{{- if not (empty $mountPath) -}}
{{- $tls := (include "githubServerTLS.config" .) | fromYaml -}}
{{- required "githubServerTLS.certificateFrom.configMapKeyRef.name is required when githubServerTLS.runnerMountPath is set" (dig "certificateFrom" "configMapKeyRef" "name" "" $tls) -}}
{{- end -}}
{{- end -}}

{{- define "githubServerTLS.certKey" -}}
{{- $mountPath := include "githubServerTLS.mountPath" . -}}
{{- if not (empty $mountPath) -}}
{{- $tls := (include "githubServerTLS.config" .) | fromYaml -}}
{{- required "githubServerTLS.certificateFrom.configMapKeyRef.key is required when githubServerTLS.runnerMountPath is set" (dig "certificateFrom" "configMapKeyRef" "key" "" $tls) -}}
{{- end -}}
{{- end -}}

{{- define "githubServerTLS.certFilePath" -}}
{{- $mountPath := include "githubServerTLS.mountPath" . -}}
{{- if not (empty $mountPath) -}}
{{- $key := include "githubServerTLS.certKey" . -}}
{{- printf "%s/%s" (trimSuffix "/" $mountPath) $key -}}
{{- end -}}
{{- end -}}

{{- define "githubServerTLS.envItems" -}}
{{- $root := .root -}}
{{- $mountPath := include "githubServerTLS.mountPath" $root -}}
{{- if not (empty $mountPath) -}}
{{- $existing := (.existingEnv | default list) -}}
{{- $hasNodeExtra := false -}}
{{- $hasRunnerUpdate := false -}}
{{- if kindIs "slice" $existing -}}
  {{- range $existing -}}
    {{- if and (kindIs "map" .) (eq ((index . "name") | default "") "NODE_EXTRA_CA_CERTS") -}}
      {{- $hasNodeExtra = true -}}
    {{- end -}}
    {{- if and (kindIs "map" .) (eq ((index . "name") | default "") "RUNNER_UPDATE_CA_CERTS") -}}
      {{- $hasRunnerUpdate = true -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if not $hasNodeExtra -}}
- name: NODE_EXTRA_CA_CERTS
  value: {{ include "githubServerTLS.certFilePath" $root | quote }}
{{ end }}
{{- if not $hasRunnerUpdate -}}
- name: RUNNER_UPDATE_CA_CERTS
  value: "1"
{{ end }}
{{- end -}}
{{- end -}}

{{- define "githubServerTLS.volumeMountItem" -}}
{{- $root := .root -}}
{{- $mountPath := include "githubServerTLS.mountPath" $root -}}
{{- if not (empty $mountPath) -}}
{{- $existing := (.existingVolumeMounts | default list) -}}
{{- $hasMount := false -}}
{{- if kindIs "slice" $existing -}}
  {{- range $existing -}}
    {{- if and (kindIs "map" .) (eq ((index . "name") | default "") "github-server-tls-cert") -}}
      {{- $hasMount = true -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if not $hasMount -}}
- name: github-server-tls-cert
  mountPath: {{ $mountPath | quote }}
  readOnly: true
{{ end }}
{{- end -}}
{{- end -}}

{{- define "githubServerTLS.podVolumeItem" -}}
{{- $mountPath := include "githubServerTLS.mountPath" . -}}
{{- if not (empty $mountPath) -}}
{{- $cmName := include "githubServerTLS.configMapName" . -}}
{{- $key := include "githubServerTLS.certKey" . -}}
- name: github-server-tls-cert
  configMap:
    name: {{ $cmName | quote }}
    items:
      - key: {{ $key | quote }}
        path: {{ $key | quote }}
{{ end }}
{{ end }}


