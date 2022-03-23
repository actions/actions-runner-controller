#!/bin/bash
if [ "${RUNNER_CUSTOM_RBAC:-}" == "true" ]; then
    export HTTPS_PROXY=
    export https_proxy=

    APISERVER=https://${KUBERNETES_SERVICE_HOST}:${KUBERNETES_SERVICE_PORT_HTTPS}
    SERVICEACCOUNT=/var/run/secrets/kubernetes.io/serviceaccount
    NAMESPACE=$(cat ${SERVICEACCOUNT}/namespace)
    TOKEN=$(cat ${SERVICEACCOUNT}/token)
    CACERT=${SERVICEACCOUNT}/ca.crt

    curl --silent -X PATCH --cacert ${CACERT} -H "Content-Type: application/merge-patch+json" -H "Authorization: Bearer ${TOKEN}" \
        ${APISERVER}/apis/actions.summerwind.dev/v1alpha1/namespaces/${NAMESPACE}/runners/${HOSTNAME}/status \
        -d "{\"status\":{\"phase\": \"$1\", \"message\": \"$2\"}}"
fi