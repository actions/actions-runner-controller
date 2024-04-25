# Monitoring and troubleshooting

> [!WARNING]
> This documentation covers the legacy mode of ARC (resources in the `actions.summerwind.net` namespace). If you're looking for documentation on the newer autoscaling runner scale sets, it is available in [GitHub Docs](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/quickstart-for-actions-runner-controller). To understand why these resources are considered legacy (and the benefits of using the newer autoscaling runner scale sets), read [this discussion (#2775)](https://github.com/actions/actions-runner-controller/discussions/2775).

## Metrics

The controller also exposes Prometheus metrics on a `/metrics` endpoint. By default this is on port `8443` behind an RBAC proxy.

If needed, the proxy can be disabled in the `values.yml` file:

```diff
metrics:
  serviceAnnotations: {}
  serviceMonitor:
    enable: false
    namespace: ""
    timeout: 30s
    interval: 1m
  serviceMonitorLabels: {}
- port: 8443
+ port: 8080
  proxy:
-   enabled: true
+   enabled: false
```

If Prometheus is available inside the cluster, then add some `podAnnotations` to begin scraping the metrics:

```diff
podAnnotations:
+ prometheus.io/scrape: "true"
+ prometheus.io/path: /metrics
+ prometheus.io/port: "8080"
```

## Dashboard

If needed, import and use Grafana dashboard [ID 19382](https://grafana.com/grafana/dashboards/19382-horizontalrunnerautoscalers/) for horizontal runner autoscalers.

This dashboard adds observability to the Horizontal Runner Autoscalers (HRA). It automatically scans some ARC metrics to display the behaviour of all your HRAs.

## Troubleshooting

See [troubleshooting guide](../TROUBLESHOOTING.md) for solutions to various problems people have run into consistently.
