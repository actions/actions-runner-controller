
{{/*
Labels applied to the controller Pod template (spec.template.metadata.labels)
*/}}
{{- define "gha-controller-template.labels" -}}
{{- $static := dict "app.kubernetes.io/part-of" "gha-rs-controller" "app.kubernetes.io/component" "controller-manager" -}}
{{- $_ := set $static "app.kubernetes.io/version" (.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-") -}}
{{- $selector := include "gha-controller.selector-labels" . | fromYaml -}}
{{- $podUser := include "apply-non-reserved-gha-labels-and-annotations" (.Values.controller.pod.metadata.labels | default (dict)) | fromYaml -}}
{{- $labels := mergeOverwrite $podUser $selector $static -}}
{{- toYaml $labels -}}
{{- end }}

{{/*
Annotations applied to the controller Pod template (spec.template.metadata.annotations)
*/}}
{{- define "gha-controller-template.annotations" -}}
{{- $static := dict "kubectl.kubernetes.io/default-container" "manager" -}}
{{- $podUser := include "apply-non-reserved-gha-labels-and-annotations" (.Values.controller.pod.metadata.annotations | default (dict)) | fromYaml -}}
{{- $annotations := mergeOverwrite $podUser $static -}}
{{- toYaml $annotations -}}
{{- end }}

{{- define "gha-controller-template.manager-container" -}}
name: manager
image: "{{ .Values.controller.manager.container.image }}"
imagePullPolicy: {{ default .Values.controller.manager.container.pullPolicy "IfNotPresent" }}
command:
  - "/manager"
args:
  - "--auto-scaling-runner-set-only"
{{- if gt (int (default 1 .Values.controller.replicaCount)) 1 }}
  - "--enable-leader-election"
  - "--leader-election-id={{ include "gha-controller.name" . }}"
{{- end }}
{{- with .Values.imagePullSecrets }}
{{- range . }}
  - "--auto-scaler-image-pull-secrets={{- .name -}}"
{{- end }}
{{- end }}
{{- with .Values.controller.manager.config.logLevel }}
  - "--log-level={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.logFormat }}
  - "--log-format={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.watchSingleNamespace }}
  - "--watch-single-namespace={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.runnerMaxConcurrentReconciles }}
  - "--runner-max-concurrent-reconciles={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.updateStrategy }}
  - "--update-strategy={{ . }}"
{{- end }}
{{- if .Values.controller.metrics }}
{{- with .Values.controller.metrics }}
  - "--listener-metrics-addr={{ .listenerAddr }}"
  - "--listener-metrics-endpoint={{ .listenerEndpoint }}"
  - "--metrics-addr={{ .controllerManagerAddr }}"
{{- end }}
{{- else }}
  - "--listener-metrics-addr=0"
  - "--listener-metrics-endpoint="
  - "--metrics-addr=0"
{{- end }}
{{- range .Values.controller.manager.config.excludeLabelPropagationPrefixes }}
  - "--exclude-label-propagation-prefix={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.k8sClientRateLimiterQPS }}
  - "--k8s-client-rate-limiter-qps={{ . }}"
{{- end }}
{{- with .Values.controller.manager.config.k8sClientRateLimiterBurst }}
  - "--k8s-client-rate-limiter-burst={{ . }}"
{{- end }}
{{- with .Values.controller.manager.container.extraArgs }}
{{- range . }}
  - "{{ . }}"
{{- end }}
{{- end }}
{{- $ports := list -}}
{{- if .Values.controller.metrics }}
{{- $metricsPort := dict "containerPort" ((regexReplaceAll ":([0-9]+)" .Values.controller.metrics.controllerManagerAddr "${1}") | int) "protocol" "TCP" "name" "metrics" -}}
{{- $ports = append $ports $metricsPort -}}
{{- end }}
{{- with .Values.controller.manager.container.extraPorts }}
{{- if kindIs "slice" . }}
{{- $ports = concat $ports . -}}
{{- end }}
{{- end }}
{{- if gt (len $ports) 0 }}
ports:
{{- toYaml $ports | nindent 2 }}
{{- end }}
env:
  - name: CONTROLLER_MANAGER_CONTAINER_IMAGE
    value: "{{ .Values.controller.manager.container.image }}"
  - name: CONTROLLER_MANAGER_POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  {{- with .Values.controller.manager.container.env }}
  {{- if kindIs "slice" . }}
{{- toYaml . | nindent 2 }}
  {{- end }}
  {{- end }}
{{- with .Values.controller.manager.container.resources }}
resources:
{{- toYaml . | nindent 2 }}
{{- end }}
{{- with .Values.controller.manager.container.securityContext }}
securityContext:
{{- toYaml . | nindent 2 }}
{{- end }}
volumeMounts:
  - mountPath: /tmp
    name: tmp
  {{- with .Values.controller.manager.container.extraVolumeMounts }}
  {{- range . }}
  - {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- end }}
  {{- range .Values.controller.pod.extraVolumeMounts }}
  - {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}