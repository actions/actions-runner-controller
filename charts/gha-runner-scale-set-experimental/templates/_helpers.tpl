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


