#!/usr/bin/env bash

# See https://developers.cloudflare.com/cloudflare-one/tutorials/many-cfd-one-tunnel/

kubectl create ns tunnel || :

kubectl -n tunnel delete secret tunnel-credentials || :

kubectl -n tunnel create secret generic tunnel-credentials \
  --from-file=credentials.json=$HOME/.cloudflared/${TUNNEL_ID}.json || :

cat <<MANIFEST | kubectl -n tunnel ${OP} -f -
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cloudflared
spec:
  selector:
    matchLabels:
      app: cloudflared
  replicas: 2 # You could also consider elastic scaling for this deployment
  template:
    metadata:
      labels:
        app: cloudflared
    spec:
      containers:
      - name: cloudflared
        image: cloudflare/cloudflared:latest
        args:
        - tunnel
        # Points cloudflared to the config file, which configures what
        # cloudflared will actually do. This file is created by a ConfigMap
        # below.
        - --config
        - /etc/cloudflared/config/config.yaml
        - run
        livenessProbe:
          httpGet:
            # Cloudflared has a /ready endpoint which returns 200 if and only if
            # it has an active connection to the edge.
            path: /ready
            port: 2000
          failureThreshold: 1
          initialDelaySeconds: 10
          periodSeconds: 10
        volumeMounts:
        - name: config
          mountPath: /etc/cloudflared/config
          readOnly: true
        # Each tunnel has an associated "credentials file" which authorizes machines
        # to run the tunnel. cloudflared will read this file from its local filesystem,
        # and it'll be stored in a k8s secret.
        - name: creds
          mountPath: /etc/cloudflared/creds
          readOnly: true
      volumes:
      - name: creds
        secret:
          secretName: tunnel-credentials
      # Create a config.yaml file from the ConfigMap below.
      - name: config
        configMap:
          name: cloudflared
          items:
          - key: config.yaml
            path: config.yaml
---
# This ConfigMap is just a way to define the cloudflared config.yaml file in k8s.
# It's useful to define it in k8s, rather than as a stand-alone .yaml file, because
# this lets you use various k8s templating solutions (e.g. Helm charts) to
# parameterize your config, instead of just using string literals.
apiVersion: v1
kind: ConfigMap
metadata:
  name: cloudflared
data:
  config.yaml: |
    # Name of the tunnel you want to run
    tunnel: ${TUNNEL_NAME}
    credentials-file: /etc/cloudflared/creds/credentials.json
    # Serves the metrics server under /metrics and the readiness server under /ready
    metrics: 0.0.0.0:2000
    # Autoupdates applied in a k8s pod will be lost when the pod is removed or restarted, so
    # autoupdate doesn't make sense in Kubernetes. However, outside of Kubernetes, we strongly
    # recommend using autoupdate.
    no-autoupdate: true
    ingress:
    # The first rule proxies traffic to the httpbin sample Service defined in app.yaml
    - hostname: ${TUNNEL_HOSTNAME}
      service: http://actions-runner-controller-github-webhook-server.actions-runner-system:80
    # This rule matches any traffic which didn't match a previous rule, and responds with HTTP 404.
    - service: http_status:404
MANIFEST

kubectl -n tunnel delete po -l app=cloudflared || :
