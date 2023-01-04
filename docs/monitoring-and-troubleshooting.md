# Monitoring and troubleshooting

## Metrics

The controller also exposes Prometheus metrics on a `/metrics` endpoint. By default this is on port `8443` behind an RBAC proxy.

If needed, the proxy can be disabled in the `values.yml` file:

```diff
metrics:
  serviceAnnotations: {}
  serviceMonitor: false
  serviceMonitorLabels: {}
+ port: 8080
  proxy:
+   enabled: false
```

If Prometheus is available inside the cluster, then add some `podAnnotations` to begin scraping the metrics:

```diff
podAnnotations:
+ prometheus.io/scrape: "true"
+ prometheus.io/path: /metrics
+ prometheus.io/port: "8080"
```

## Troubleshooting

See [troubleshooting guide](../TROUBLESHOOTING.md) for solutions to various problems people have run into consistently.