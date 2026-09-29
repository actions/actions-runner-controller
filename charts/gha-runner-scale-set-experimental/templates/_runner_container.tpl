{{/*
Merge list entries by their Kubernetes identity, replacing whole entries rather
than merging maps (an EnvVar must not acquire both value and valueFrom).
User entries precede defaults, as in the standard runner chart.
*/}}
{{- define "runner-container.merge-list" -}}
{{- $key := .key -}}
{{- $path := .path -}}
{{- $seen := dict -}}
{{- $out := list -}}
{{- range $group := list .overrides .defaults -}}
  {{- $groupSeen := dict -}}
  {{- range $group -}}
    {{- if not (kindIs "map" .) -}}
      {{- fail (printf "%s must contain objects with a %s" $path $key) -}}
    {{- end -}}
    {{- $identity := index . $key -}}
    {{- if or (not (kindIs "string" $identity)) (empty $identity) -}}
      {{- fail (printf "%s entries must have a non-empty %s" $path $key) -}}
    {{- end -}}
    {{- if hasKey $groupSeen $identity -}}
      {{- fail (printf "%s contains duplicate %s %q" $path $key $identity) -}}
    {{- end -}}
    {{- $_ := set $groupSeen $identity true -}}
    {{- if not (hasKey $seen $identity) -}}
      {{- $out = append $out . -}}
      {{- $_ := set $seen $identity true -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}

{{/*
All modes share the documented runner.container customization surface.
Keep Values immutable: mode and TLS defaults are built separately and only a
deep copy of the user's container is modified.
*/}}
{{- define "runner-container.render" -}}
{{- $root := .root -}}
{{- $container := $root.Values.runner.container | default dict -}}
{{- if not (kindIs "map" $container) -}}
  {{- fail "runner.container must be a map/object" -}}
{{- end -}}
{{- range $field := list "env" "volumeMounts" "args" -}}
  {{- if and (hasKey $container $field) (not (kindIs "slice" (index $container $field))) -}}
    {{- fail (printf "runner.container.%s must be a list" $field) -}}
  {{- end -}}
{{- end -}}
{{- range $field := list "resources" "securityContext" -}}
  {{- if and (hasKey $container $field) (not (kindIs "map" (index $container $field))) -}}
    {{- fail (printf "runner.container.%s must be a map/object" $field) -}}
  {{- end -}}
{{- end -}}
{{- if hasKey $container "volumes" -}}
  {{- fail "runner.container.volumes is not supported; use runner.pod.spec.volumes" -}}
{{- end -}}
{{- $out := deepCopy (omit $container "name" "image" "command" "env" "volumeMounts") -}}
{{- $_ := set $out "name" "runner" -}}
{{- $_ := set $out "image" (include "runner.image" $root) -}}
{{- $_ := set $out "command" (include "runner.command" $root | fromJsonArray) -}}
{{- $env := $container.env | default list -}}
{{- if .legacyEnv -}}
  {{- if not (kindIs "slice" .legacyEnv) -}}
    {{- fail "runner.env must be a list; use runner.container.env" -}}
  {{- end -}}
  {{- $env = include "runner-container.merge-list" (dict "key" "name" "path" "runner.container.env" "overrides" $env "defaults" .legacyEnv) | fromYamlArray -}}
{{- end -}}
{{- $env = include "runner-container.merge-list" (dict "key" "name" "path" "runner.container.env" "overrides" $env "defaults" (.env | default list)) | fromYamlArray -}}
{{- $tlsEnv := include "githubServerTLS.envItems" (dict "root" $root "existingEnv" $env) | fromYamlArray -}}
{{- $env = concat $env $tlsEnv -}}
{{- if $env -}}
  {{- $_ := set $out "env" $env -}}
{{- end -}}
{{- $mounts := include "runner-container.merge-list" (dict "key" "mountPath" "path" "runner.container.volumeMounts" "overrides" ($container.volumeMounts | default list) "defaults" (.volumeMounts | default list)) | fromYamlArray -}}
{{- $tlsMounts := include "githubServerTLS.volumeMountItem" (dict "root" $root "existingVolumeMounts" $mounts) | fromYamlArray -}}
{{- $mounts = include "runner-container.merge-list" (dict "key" "mountPath" "path" "runner.container.volumeMounts" "overrides" $mounts "defaults" $tlsMounts) | fromYamlArray -}}
{{- if $mounts -}}
  {{- $_ := set $out "volumeMounts" $mounts -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}
