{{/*
The bind address must retain its host in --metrics-addr, but containerPort only
accepts a numeric port. "0" disables the server and must not create a port.
*/}}
{{- define "gha-controller.metrics-port" -}}
{{- $address := . -}}
{{- $error := "controller.metrics.controllerManagerAddr must be \"0\" or a host:port address with a numeric port between 1 and 65535 (IPv6 hosts must be bracketed)" -}}
{{- if ne (toString $address) "0" -}}
  {{- if not (kindIs "string" $address) -}}
    {{- fail $error -}}
  {{- end -}}
  {{- if not (regexMatch `^(\[[^]]+\]|[^:]*):[0-9]+$` $address) -}}
    {{- fail $error -}}
  {{- end -}}
  {{- $portString := regexFind "[0-9]+$" $address -}}
  {{- $port := atoi $portString -}}
  {{- if or (lt $port 1) (gt $port 65535) -}}
    {{- fail $error -}}
  {{- end -}}
  {{- $host := trimSuffix (printf ":%s" $portString) $address -}}
  {{- if hasPrefix "[" $host -}}
    {{- $ip := trimSuffix "]" (trimPrefix "[" $host) -}}
    {{- $zone := splitList "%" $ip -}}
    {{- if gt (len $zone) 2 -}}
      {{- fail $error -}}
    {{- end -}}
    {{- if eq (len $zone) 2 -}}
      {{- if not (regexMatch "^[A-Za-z0-9_.-]+$" (index $zone 1)) -}}
        {{- fail $error -}}
      {{- end -}}
    {{- end -}}
    {{- $ip = index $zone 0 -}}
    {{- if contains "." $ip -}}
      {{- $ipv4 := last (splitList ":" $ip) -}}
      {{- include "gha-controller.validate-ipv4" (dict "ip" $ipv4 "error" $error) -}}
      {{- $ip = printf "%s0:0" (trimSuffix $ipv4 $ip) -}}
    {{- end -}}
    {{- if or (not (regexMatch "^[0-9A-Fa-f:]+$" $ip)) (contains ":::" $ip) (and (hasPrefix ":" $ip) (not (hasPrefix "::" $ip))) (and (hasSuffix ":" $ip) (not (hasSuffix "::" $ip))) -}}
      {{- fail $error -}}
    {{- end -}}
    {{- $halves := splitList "::" $ip -}}
    {{- $groups := splitList ":" $ip -}}
    {{- $count := 0 -}}
    {{- range $groups -}}
      {{- if ne . "" -}}
        {{- if not (regexMatch "^[0-9A-Fa-f]{1,4}$" .) -}}
          {{- fail $error -}}
        {{- end -}}
        {{- $count = add1 $count -}}
      {{- end -}}
    {{- end -}}
    {{- if eq (len $halves) 1 -}}
      {{- if or (ne $count 8) (hasPrefix ":" $ip) (hasSuffix ":" $ip) -}}
        {{- fail $error -}}
      {{- end -}}
    {{- else if or (ne (len $halves) 2) (ge $count 8) -}}
      {{- fail $error -}}
    {{- end -}}
  {{- else if ne $host "" -}}
    {{- if or (gt (len $host) 253) (not (regexMatch `^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*\.?$` $host)) -}}
      {{- fail $error -}}
    {{- end -}}
    {{- range splitList "." $host -}}
      {{- if gt (len .) 63 -}}
        {{- fail $error -}}
      {{- end -}}
    {{- end -}}
    {{- if regexMatch `^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$` $host -}}
      {{- include "gha-controller.validate-ipv4" (dict "ip" $host "error" $error) -}}
    {{- end -}}
  {{- end -}}
  {{- $port -}}
{{- end -}}
{{- end -}}

{{- define "gha-controller.validate-ipv4" -}}
{{- $error := .error -}}
{{- $parts := splitList "." .ip -}}
{{- if ne (len $parts) 4 -}}
  {{- fail $error -}}
{{- end -}}
{{- range $parts -}}
  {{- if or (not (regexMatch "^(0|[1-9][0-9]{0,2})$" .)) (gt (int .) 255) -}}
    {{- fail $error -}}
  {{- end -}}
{{- end -}}
{{- end -}}
