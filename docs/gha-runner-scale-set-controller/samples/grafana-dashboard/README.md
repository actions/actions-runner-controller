# Visualizing Autoscaling Runner Scale Set metrics with Grafana

With the metrics support introduced in [gha-runner-scale-set-0.5.0](https://github.com/actions/actions-runner-controller/releases/tag/gha-runner-scale-set-0.5.0), you can visualize the autoscaling behavior of your runner scale set with your tool of choice. 

This sample dashboard shows how to visualize the metrics with [Grafana](https://grafana.com/).

> [!NOTE]
> We do not intend to provide a supported ARC dashboard. This is simply a reference and a demonstration for how you could leverage the metrics emitted by the controller-manager and listeners to visualize the autoscaling behavior of your runner scale set. We offer no promises of future upgrades to this sample.

## Demo

![Grafana dashboard example](grafana-sample.png)

## Setup

1. Make sure to have [Grafana](https://grafana.com/docs/grafana/latest/installation/) and [Prometheus](https://prometheus.io/docs/prometheus/latest/installation/) running in your cluster.
2. Make sure that Prometheus is properly scraping the metrics endpoints of the controller-manager and listeners.
3. Import the [dashboard](ARC-Autoscaling-Runner-Set-Monitoring.json) into Grafana.

## Required metrics

This sample relies on the suggestion listener metrics configuration in the scale set [values.yaml](https://github.com/actions/actions-runner-controller/blob/ea27448da51385470b1ce67150aa695cfa45fd3f/charts/gha-runner-scale-set/values.yaml#L129-L270).

The following metrics are required to be scraped by Prometheus in order to populate the dashboard:

| Metric | Required labels | Source |
| ------ | ----------- | -----|
| container_fs_writes_bytes_total | namespace | cAdvisor
| container_fs_reads_bytes_total | namespace | cAdvisor
| container_memory_working_set_bytes | namespace | cAdvisor
| controller_runtime_active_workers | controller | ARC Controller
| controller_runtime_reconcile_time_seconds_sum | namespace | ARC Controller
| controller_runtime_reconcile_errors_total | namespace | ARC Controller
| gha_assigned_jobs | actions_github_com_scale_set_name, namespace | ARC Controller
| gha_controller_failed_ephemeral_runners | name, namespace | ARC Controller
| gha_controller_pending_ephemeral_runners | name, namespace | ARC Controller
| gha_controller_running_ephemeral_runners | name, namespace | ARC Controller
| gha_controller_running_listeners | namespace | ARC Controller
| gha_desired_runners | actions_github_com_scale_set_name, namespace | ARC Listener
| gha_idle_runners | actions_github_com_scale_set_name, namespace | ARC Listener
| gha_job_execution_duration_seconds_bucket | actions_github_com_scale_set_name, actions_github_com_scale_set_namespace | ARC Listener
| gha_job_startup_duration_seconds_bucket | actions_github_com_scale_set_name, actions_github_com_scale_set_namespace | ARC Listener
| gha_registered_runners | actions_github_com_scale_set_name, namespace | ARC Listener
| gha_running_jobs | actions_github_com_scale_set_name, actions_github_com_scale_set_namespace | ARC Listener
| kube_pod_container_status_ready | namespace | kube-state-metrics
| kube_pod_container_status_terminated_reason | namespace, reason | kube-state-metrics
| kube_pod_container_status_waiting | namespace | kube-state-metrics
| rest_client_requests_total | code, method, namespace | ARC Controller
| scrape_duration_seconds | | prometheus
| workqueue_depth | name, namespace | ARC Controller
| workqueue_queue_duration_seconds_sum | namespace | ARC Controller

## Details

This dashboard demonstrates some of the metrics provided by ARC and the underlying Kubernetes runtime. It provides a sample visualization of the behavior of the runner scale set, the ARC controllers, and the listeners. This should not be considered a comprehensive dashboard; it is a starting point that can be used with other metrics and logs to understand the health of the cluster. Review the [GitHub documentation detailing the Actions Runner Controller metrics and how to enable them](https://docs.github.com/en/enterprise-server@3.10/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller/deploying-runner-scale-sets-with-actions-runner-controller#enabling-metrics).

The dashboard includes the following metrics:

| Label                            | Description                                         |
| -------------------------------- | ----------------------------------------------------|
| Startup Duration                 | Heat map of the wait time before a job starts, with the colors indicating the increase in the number of jobs in that time bucket. An increasing time can indicate that the cluster is resource constrained and may need additional nodes or resources to handle the load. |
| Execution Duration                 | Heat map of the execution time for a job, with the colors indicating the increase in the number of jobs in that time bucket. Time can be affected by the number of steps in the job, the allocated CPU, and whether there is resource contention on the node that is impacting performance |
| Assigned Jobs                    | The number of jobs that have been assigned to the listener. This is the number of jobs that the listener is responsible for providing a runner to process. |
| Desired Runners                  | The number of runners that the listener is requesting from the controller. This is the number of runners required to process the assigned jobs and provide idle runners. It is limited by the configured maximum runner count for the scale set. |
| Idle Runners                     | The total number of ephemeral runners that are available to accept jobs across all selected scale sets. Keeping a pool of idle runners can enable a faster start time under load, but excessive idle runners will consume resources and can prevent nodes from scaling down. |
| Running Jobs | The number of runners that are currently processing jobs. |
| Failed Runners                   | The total number of ephemeral runners that have failed to properly start. This may require reviewing the custom resource and logs to identify and resolve the root causes. Common causes include resource issues and failure to pull the required image. |
| Listeners                 | The number of listeners currently running and attempting to manage jobs for the scale set. This should match the number of scale sets deployed. |
| Pending Runners                  | The total number of ephemeral runners that ARC has requested and is waiting for Kubernetes to provide in a running state. If the Kubernetes API server is responsive, this will typically match the number of runner pods that are in a pending state. This number includes requests for runner pods that have not yet been scheduled. When this number is higher than the number of runner pods in a pending state, it can indicate performance issues. |
| Registered Runners               | The total number of ephemeral runners that have been successfully registered. |
| Active Runners | The total number of runners that are active and either available or processing jobs. |
| Out of Memory | The number of containers that have been terminated by the OOMKiller. This can indicate that the requests/ limits for one or more pods on the node were configured improperly, allowing pods to request more memory than the node had available. |
| Peak Container Memory | The maximum amount of memory used by any container in a given namespace during the selected time. This can be used for tuning the memory limits for the pods and for alerts as containers get close to their limits.
| Container I/O | Shows the number of bytes read and written to the container filesystem. This can be used to identify if the container is reading or writing a large amount of data to the filesystem, which can impact performance. |
| Container Pod Status | Shows the number of containers in each status (waiting, running, terminated, ready). This can be used to identify if there are a large number of containers that are failing to start or are in a waiting state. |
| Reconcile time              | The time to perform a single reconciliation task from a controller's work queue. This metric reflects the time it takes for ARC to complete each step in the processing of creating, managing, and cleaning up runners. As this increases, it can indicate resource contention, processing delays, or delays from the API server. |
| Workqueue Queue Duration | The time items spent in the work queue for a controller before being processed. This is often related to the work queue depth; as the number of items increases, it can take an increasing amount of time for an item to be processed. |
| Reconciliation errors            | Reconciliation is the process of a controller ensuring the desired state and actual state of the resources match. Each time an event occurs on a resource watched by the controller, the controller is required to indicate if the new state matches the desired state. Kubernetes adds a task to the work queue for the controller to perform this reconciliation. Errors indicate that controller has not achieved a desired state and is requesting Kubernetes to queue another request for reconciliation. Ideally, this number remains close to zero. An increasing number can indicate resource contention or delays processing API server requests. This reflects Kubernetes resources that ARC is waiting to be provided or in the necessary state. As a concrete example, ARC will request the creation of a secret prior to creating the pod. If the response indicates the secret is not immediately ready, ARC will requeue the reconciliation task with the error details, incrementing this count. |
| Workqueue depth                  | The number of tasks that Kubernetes has queued for the ARC controllers to process. This includes reconciliation requests and tasks initiated by the controller. Managing a runner requires multiple steps to prepare, create, update, and delete the runner, its resources, and the ARC custom resources. As each step is completed (or trigger reconciliation), new tasks are queued for processing. The controller will then use one or more workers to process these tasks in the order they were queued. As the depth increases, it indicates more tasks awaiting time from the controller. Growth indicates increasing work and may reflect Kubernetes resource contention or processing latencies. Each request for a new runner will result in multiple tasks being added to the work queue to prepare and create the runner and the related ARC custom resources. |
| Active Workers | The number of workers that are actively processing tasks in the work queue. If the queue is empty, then there may be no workers required to process the tasks. The number of workers for the ephemeral runner is configurable in the scale set values file. |
| API Calls | Shows the number of calls to the API server by status code and HTTP method. The method indicates the type of activity being performed, while the status code indicates the result of the activity. Error codes of 500 and above often indicate a Kubernetes issue. | 
| Scrape Duration (seconds)        | The amount of time required for Prometheus to read the configured metrics from components in the cluster. An increasing number may indicate a lack of resources for Prometheus and a risk of the process exceeding the configured timeout, leading to lost metrics data.  | 
