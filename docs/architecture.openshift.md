# OpenShift-Specific Architecture

This document covers components and configuration that exist only in the OpenShift build of func-operator. It complements the [architecture](architecture.md) document, which is synced from the upstream [functions-dev/func-operator](https://github.com/functions-dev/func-operator) repository. For usage, see [README.openshift.md](../README.openshift.md).

## ConsolePlugin Controller

**ConsolePluginReconciler** (`internal/controller/consoleplugin_controller.go`) manages the Functions console plugin: its ConsolePlugin, Deployment, Service, ServiceAccount, Role, and RoleBinding. It is set up only when the operator's `CONSOLE_PLUGIN_IMAGE` environment variable is set and the `console.openshift.io/v1` ConsolePlugin API is available. It watches the `func-operator-controller-config` ConfigMap and the plugin's ConsolePlugin, Deployment, Service, and ServiceAccount. When `consolePluginEnabled` is `"true"` in the ConfigMap it creates or updates the plugin resources; otherwise it deletes them.

## Configuration

Besides the keys in the [architecture configuration](architecture.md#configuration), the controller reads this console plugin setting from the `func-operator-controller-config` ConfigMap:

| Key | Description | Default |
|-----|-------------|---------|
| `workflowPollingInterval` | Interval at which the console plugin polls GitHub workflow runs, passed verbatim as `--workflow-polling-interval`. Must be a positive Go duration (for example `10s`); while the console plugin is enabled, invalid values fail reconciliation without changing the plugin Deployment. Requires the operator's `CONSOLE_PLUGIN_IMAGE` to reference a plugin image that supports the flag | unset (plugin default `3s`) |
