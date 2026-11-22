# func-operator on OpenShift

This file documents behavior that exists only in the OpenShift build of func-operator ([openshift/functions-operator](https://github.com/openshift/functions-operator)). It complements the [README](README.md), which is synced from the upstream [functions-dev/func-operator](https://github.com/functions-dev/func-operator) repository. For the OpenShift-specific architecture, see [docs/architecture.openshift.md](docs/architecture.openshift.md).

## Functions Console Plugin

On OpenShift, the operator deploys the Functions console plugin ([openshift/faas-console-plugin](https://github.com/openshift/faas-console-plugin)) when its `CONSOLE_PLUGIN_IMAGE` environment variable is set, the `ConsolePlugin` API is available, and `consolePluginEnabled` is `"true"`.

### Configuration

OpenShift-specific keys in the `func-operator-controller-config` ConfigMap, in the operator's namespace (`func-operator-system` in the OpenShift manifests). The `autoUpdateMiddleware` key is described in the [README](README.md).

| Key | Description | Default |
|-----|-------------|---------|
| `consolePluginEnabled` | Deploy the Functions console plugin (`"true"`/`"false"`) | `"true"` in the ConfigMap the operator creates at startup (`--create-config`, set by the OpenShift manifests); a missing key disables the plugin |
| `ghApiUrl` | GitHub API base URL for the plugin, passed as `--gh-api-url` | unset (falls back to the operator's `GH_API_URL` env; if neither is set, no flag is passed and the plugin uses github.com) |
| `workflowPollingInterval` | Interval at which the plugin polls GitHub workflow runs, passed as `--workflow-polling-interval` (positive Go duration, e.g. `10s`; while the plugin is enabled, invalid values fail reconciliation) | unset (plugin default `3s`) |

Example:

```bash
kubectl patch configmap func-operator-controller-config -n func-operator-system \
  --type merge -p '{"data":{"workflowPollingInterval":"10s"}}'
```

Note: `workflowPollingInterval` requires a plugin image (`CONSOLE_PLUGIN_IMAGE`) that supports `--workflow-polling-interval`; older images reject the flag and fail to start.
