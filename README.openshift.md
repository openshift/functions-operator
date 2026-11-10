# func-operator on OpenShift

This file documents behavior that exists only in the OpenShift build of func-operator ([openshift/functions-operator](https://github.com/openshift/functions-operator)). It complements the [README](README.md), which is synced from the upstream [functions-dev/func-operator](https://github.com/functions-dev/func-operator) repository. For the OpenShift-specific architecture, see [docs/architecture.openshift.md](docs/architecture.openshift.md).

## Functions Console Plugin

On OpenShift, the operator deploys the Functions console plugin ([openshift/faas-console-plugin](https://github.com/openshift/faas-console-plugin)) when its `CONSOLE_PLUGIN_IMAGE` environment variable is set, the `ConsolePlugin` API is available, and `consolePluginEnabled` is `"true"` in the `func-operator-controller-config` ConfigMap.

### Configuring the Console Plugin Workflow Polling Interval

The console plugin polls GitHub for the latest workflow run of each function repository every 3 seconds by default. To change the interval, add `workflowPollingInterval` to the existing `func-operator-controller-config` ConfigMap, keeping its other keys:

```bash
kubectl patch configmap func-operator-controller-config -n func-operator-system \
  --type merge -p '{"data":{"workflowPollingInterval":"10s"}}'
```

The value must be a positive Go duration such as `10s`, `1m`, or `1m30s`, and the operator passes it to the plugin unchanged as `--workflow-polling-interval`. When the key is unset or empty, the operator passes no flag and the plugin keeps its default. While the console plugin is enabled, a malformed, zero, or negative value fails reconciliation with an error naming the key, and the plugin Deployment is left unchanged.

Setting the key requires the operator's `CONSOLE_PLUGIN_IMAGE` environment variable to reference a console plugin image that supports `--workflow-polling-interval`. Older plugin images reject the unknown flag and fail to start.
