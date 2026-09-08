{{/*
Render a labels or annotations map with all values coerced to strings.
Kubernetes only accepts string values, so scalars such as `true` or `1` must not be
rendered as YAML booleans or numbers.
*/}}
{{- define "string-map" -}}
{{- $out := dict -}}
{{- range $k, $v := . -}}
{{- $_ := set $out $k (printf "%v" $v) -}}
{{- end -}}
{{- toYaml $out -}}
{{- end }}

{{/*
Validate a label or annotation key against the Kubernetes qualified name rules.
Expects a dict with "key", "kind" (label|annotation) and "path" (the values path used in the error message).
*/}}
{{- define "validate-metadata-key" -}}
{{- $key := .key -}}
{{- $parts := splitList "/" $key -}}
{{- $name := $key -}}
{{- if gt (len $parts) 2 -}}
{{- fail (printf "%s: invalid %s key %q: a qualified name must consist of an optional DNS subdomain prefix followed by a single '/'" .path .kind $key) -}}
{{- end -}}
{{- if eq (len $parts) 2 -}}
{{- $prefix := index $parts 0 -}}
{{- $name = index $parts 1 -}}
{{- if or (eq $prefix "") (gt (len $prefix) 253) -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain of no more than 253 characters" .path .kind $key $prefix) -}}
{{- end -}}
{{- range $_, $seg := splitList "." $prefix -}}
{{- if or (eq $seg "") (gt (len $seg) 63) (not (regexMatch "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$" $seg)) -}}
{{- fail (printf "%s: invalid %s key %q: the prefix %q must be a DNS subdomain of no more than 253 characters" .path .kind $key $prefix) -}}
{{- end -}}
{{- end -}}
{{- if or (eq $name "") (gt (len $name) 63) (not (regexMatch "^[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$" $name)) -}}
{{- fail (printf "%s: invalid %s key %q: the name part must be no more than 63 characters, consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" .path .kind $key) -}}
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
{{- $metadata := .metadata | default dict -}}
{{- if kindIs "map" $metadata -}}
{{- range $key, $value := ((index $metadata "labels") | default dict) -}}
{{- include "validate-metadata-key" (dict "key" $key "kind" "label" "path" (printf "%s.labels" $path)) -}}
{{- $rendered := printf "%v" $value -}}
{{- if gt (len $rendered) 63 -}}
{{- fail (printf "%s.labels: invalid value %q for label %q: a label value must be no more than 63 characters" $path $rendered $key) -}}
{{- end -}}
{{- if not (regexMatch "^(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?$" $rendered) -}}
{{- fail (printf "%s.labels: invalid value %q for label %q: a valid label value must be an empty string or consist of alphanumeric characters, '-', '_' or '.', and must start and end with an alphanumeric character" $path $rendered $key) -}}
{{- end -}}
{{- end -}}
{{- range $key, $value := ((index $metadata "annotations") | default dict) -}}
{{- include "validate-metadata-key" (dict "key" $key "kind" "annotation" "path" (printf "%s.annotations" $path)) -}}
{{- end -}}
{{- end -}}
{{- end }}

{{/*
Validate every label and annotation map the chart can render onto resources it manages.
*/}}
{{- define "validate-all-metadata" -}}
{{- range $resource, $config := (.Values.resource | default dict) }}
{{- if kindIs "map" $config }}
{{- include "validate-metadata" (dict "metadata" (index $config "metadata") "path" (printf ".Values.resource.%s.metadata" $resource)) -}}
{{- end }}
{{- end }}
{{- $runnerPod := (index (.Values.runner | default dict) "pod") | default dict }}
{{- if kindIs "map" $runnerPod }}
{{- include "validate-metadata" (dict "metadata" (index $runnerPod "metadata") "path" ".Values.runner.pod.metadata") -}}
{{- end }}
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
    {{- $_ := set $processed $key (printf "%v" $value) -}}
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


