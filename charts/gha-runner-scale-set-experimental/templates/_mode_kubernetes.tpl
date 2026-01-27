{{- define "runner-mode-kubernetes.runner-container" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $kubeMode := (index $runner "kubernetesMode" | default dict) -}}
{{- $hookPath := (index $kubeMode "hookPath" | default "/home/runner/k8s/index.js") -}}
{{- $extensionRef := (index $kubeMode "extensionRef" | default "") -}}
{{- $extension := (index $kubeMode "extension" | default dict) -}}
{{- $extensionYaml := "" -}}
{{- if kindIs "map" $extension -}}
  {{- $extensionYaml = (index $extension "yaml" | default "") -}}
{{- end -}}
{{- $hasExtension := or (not (empty $extensionRef)) (not (empty $extensionYaml)) -}}
{{- $hookTemplatePath := printf "%s/hook-template.yaml" (dir $hookPath) -}}
{{- $setHookTemplateEnv := true -}}
{{- $userEnv := (.Values.runner.env | default list) -}}
{{- if kindIs "slice" $userEnv -}}
  {{- range $userEnv -}}
    {{- if and (kindIs "map" .) (eq ((index . "name") | default "") "ACTIONS_RUNNER_CONTAINER_HOOK_TEMPLATE") -}}
      {{- $setHookTemplateEnv = false -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if not (kindIs "string" $hookPath) -}}
  {{- fail "runner.kubernetesMode.hookPath must be a string" -}}
{{- end -}}
{{- if not (kindIs "string" $extensionRef) -}}
  {{- fail "runner.kubernetesMode.extensionRef must be a string" -}}
{{- end -}}
{{- if and (empty $extensionRef) (hasKey $kubeMode "extension") (not (kindIs "map" $extension)) -}}
  {{- fail "runner.kubernetesMode.extension must be an object when runner.kubernetesMode.extensionRef is empty" -}}
{{- end -}}
{{- if and (empty $extensionRef) (not (kindIs "string" $extensionYaml)) -}}
  {{- fail "runner.kubernetesMode.extension.yaml must be a string" -}}
{{- end -}}
{{- $requireJobContainer := true -}}
{{- if hasKey $kubeMode "requireJobContainer" -}}
  {{- $requireJobContainer = (index $kubeMode "requireJobContainer") -}}
{{- end -}}
{{- if not (kindIs "bool" $requireJobContainer) -}}
  {{- fail "runner.kubernetesMode.requireJobContainer must be a bool" -}}
{{- end -}}
name: runner
image: {{ include "runner.image" . | quote }}
command: {{ include "runner.command" . }}
env:
  - name: ACTIONS_RUNNER_CONTAINER_HOOKS
    value: {{ $hookPath | quote }}
  - name: ACTIONS_RUNNER_POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: ACTIONS_RUNNER_REQUIRE_JOB_CONTAINER
    value: {{ ternary "true" "false" $requireJobContainer | quote }}
  {{- if and $hasExtension $setHookTemplateEnv }}
  - name: ACTIONS_RUNNER_CONTAINER_HOOK_TEMPLATE
    value: {{ $hookTemplatePath | quote }}
  {{- end }}
  {{- with .Values.runner.env }}
    {{- toYaml . | nindent 2 }}
  {{- end }}
volumeMounts:
  - name: work
    mountPath: /home/runner/_work
  {{- if $hasExtension }}
  - name: hook-extension
    mountPath: {{ $hookTemplatePath | quote }}
    subPath: extension
    readOnly: true
  {{- end }}
{{- end }}

{{- define "runner-mode-kubernetes.pod-volumes" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $kubeMode := (index $runner "kubernetesMode" | default dict) -}}
{{- $extensionRef := (index $kubeMode "extensionRef" | default "") -}}
{{- $extension := (index $kubeMode "extension" | default dict) -}}
{{- $extensionYaml := "" -}}
{{- if kindIs "map" $extension -}}
  {{- $extensionYaml = (index $extension "yaml" | default "") -}}
{{- end -}}
{{- $hasExtension := or (not (empty $extensionRef)) (not (empty $extensionYaml)) -}}
{{- $claim := (index $kubeMode "workVolumeClaim" | default dict) -}}
{{- if and (not (empty $claim)) (not (kindIs "map" $claim)) -}}
  {{- fail "runner.kubernetesMode.workVolumeClaim must be a map/object" -}}
{{- end -}}
{{- if not (kindIs "string" $extensionRef) -}}
  {{- fail "runner.kubernetesMode.extensionRef must be a string" -}}
{{- end -}}
{{- if and (empty $extensionRef) (hasKey $kubeMode "extension") (not (kindIs "map" $extension)) -}}
  {{- fail "runner.kubernetesMode.extension must be an object when runner.kubernetesMode.extensionRef is empty" -}}
{{- end -}}
{{- if and (empty $extensionRef) (not (kindIs "string" $extensionYaml)) -}}
  {{- fail "runner.kubernetesMode.extension.yaml must be a string" -}}
{{- end -}}
{{- $defaultClaim := dict "accessModes" (list "ReadWriteOnce") "storageClassName" "local-path" "resources" (dict "requests" (dict "storage" "1Gi")) -}}
{{- $claimSpec := mergeOverwrite $defaultClaim $claim -}}
- name: work
  ephemeral:
    volumeClaimTemplate:
      spec:
        {{- toYaml $claimSpec | nindent 8 }}
{{- if $hasExtension }}
- name: hook-extension
  configMap:
    name: {{ if not (empty $extensionRef) }}{{ $extensionRef | quote }}{{ else }}{{ include "kube-mode-extension.name" . | quote }}{{ end }}
{{- end }}

{{- end }}

{{/*
Create the annotations for the kubernetes-mode ServiceAccount.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.kubernetesModeServiceAccount.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "kube-mode-serviceaccount.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeServiceAccount.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}

{{/*
Create the labels for the kubernetes-mode ServiceAccount.
*/}}
{{- define "kube-mode-serviceaccount.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "kube-mode-serviceaccount" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeServiceAccount.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}

{{/*
Create the labels for the kubernetes-mode Role.
*/}}
{{- define "kube-mode-role.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "kube-mode-role" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeRole.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}

{{/*
Create the annotations for the kubernetes-mode RoleBinding.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.kubernetesModeRoleBinding.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "kube-mode-role-binding.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeRoleBinding.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}

{{/*
Create the labels for the kubernetes-mode RoleBinding.
*/}}
{{- define "kube-mode-role-binding.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "kube-mode-role-binding" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $userLabels := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeRoleBinding.metadata.labels | default (dict)) | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $userLabels $resourceLabels $commonLabels) }}
{{- end }}

{{/*
Create the annotations for the kubernetes-mode Role.

Order of precedence:
1) resource.all.metadata.annotations
2) resource.kubernetesModeRole.metadata.annotations
Reserved annotations are excluded from both levels.
*/}}
{{- define "kube-mode-role.annotations" -}}
{{- $global := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.annotations | default (dict))) | fromYaml -}}
{{- $resource := (include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.kubernetesModeRole.metadata.annotations | default (dict))) | fromYaml -}}
{{- $annotations := mergeOverwrite $global $resource -}}
{{- if not (empty $annotations) -}}
  {{- toYaml $annotations }}
{{- end }}
{{- end }}

{{- define "kube-mode-extension.name" -}}
{{- $runner := (.Values.runner | default dict) -}}
{{- $kubeMode := (index $runner "kubernetesMode" | default dict) -}}
{{- $extension := (index $kubeMode "extension" | default dict) -}}
{{- $meta := (index $extension "metadata" | default dict) -}}
{{- $name := (index $meta "name" | default "") -}}
{{- if not (kindIs "string" $name) -}}
  {{- fail "runner.kubernetesMode.extension.metadata.name must be a string" -}}
{{- end -}}
{{- default (printf "%s-hook-extension" (include "autoscaling-runner-set.name" .) | trunc 63 | trimSuffix "-") $name -}}
{{- end }}

{{/*
Create the labels for the hook extension ConfigMap.
*/}}
{{- define "kube-mode-extension.labels" -}}
{{- $resourceLabels := dict "app.kubernetes.io/component" "hook-extension" -}}
{{- $commonLabels := include "gha-common-labels" . | fromYaml -}}
{{- $global := include "apply-non-reserved-gha-labels-and-annotations" (.Values.resource.all.metadata.labels | default (dict)) | fromYaml -}}
{{- toYaml (mergeOverwrite $global $resourceLabels $commonLabels) -}}
{{- end }}