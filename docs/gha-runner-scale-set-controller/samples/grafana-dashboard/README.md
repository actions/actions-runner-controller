# Visualizing Autoscaling Runner Scale Set metrics with Grafana

With metrics introduced in [gha-runner-scale-set-0.5.0](https://github.com/actions/actions-runner-controller/releases/tag/gha-runner-scale-set-0.5.0), you can now visualize the autoscaling behavior of your runner scale set with your tool of choice. This sample shows how to visualize the metrics with [Grafana](https://grafana.com/).

## Demo

![Grafana dashboard example](grafana-sample.png)

## Setup

We do not intend to provide a supported ARC dashboard. This is simply a reference and a demonstration for how you could leverage the metrics emitted by the controller-manager and listeners to visualize the autoscaling behavior of your runner scale set. We offer no promises of future upgrades to this sample.

1. Make sure to have [Grafana](https://grafana.com/docs/grafana/latest/installation/) and [Prometheus](https://prometheus.io/docs/prometheus/latest/installation/) running in your cluster.
2. Make sure that Prometheus is properly scraping the metrics endpoints of the controller-manager and listeners.
3. Import the [dashboard](ARC-Autoscaling-Runner-Set-Monitoring_1692627561838.json) into Grafana.
