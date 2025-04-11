#!/bin/bash

cd "$(dirname "${BASH_SOURCE[0]}")"

# Create templates directory
mkdir -p ../templates

# Copy CRDs from actions-runner-controller/crds to templates
cp -r ../../actions-runner-controller/crds/* ../templates
FILES=$(find ../templates -name "*.yaml")

# Add annotations template block to CRDss
for file in "${FILES[@]}"; do
    sed -i '/^  annotations:$/a{{- if .Values.keep }}\n    helm.sh/resource-policy: keep\n{{- end }}\n{{- with .Values.annotations }}\n{{- toYaml . | nindent 4 }}\n{{- end }}' $file
done
