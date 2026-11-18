# OpenShift-Specific Architecture

This document covers components that exist only in the OpenShift build of func-operator. It complements the [architecture](architecture.md) document, which is synced from the upstream [functions-dev/func-operator](https://github.com/functions-dev/func-operator) repository. The OpenShift-specific ConfigMap keys are listed in the [README.openshift.md configuration](../README.openshift.md#configuration).

## ConsolePlugin Controller

**ConsolePluginReconciler** (`internal/controller/consoleplugin_controller.go`) manages the Functions console plugin: its ConsolePlugin, Deployment, Service, ServiceAccount, Role, and RoleBinding. It is set up only when the operator's `CONSOLE_PLUGIN_IMAGE` environment variable is set and the `console.openshift.io/v1` ConsolePlugin API is available. It watches the `func-operator-controller-config` ConfigMap and the plugin's ConsolePlugin, Deployment, Service, and ServiceAccount. When `consolePluginEnabled` is `"true"` in the ConfigMap it creates or updates the plugin resources; otherwise it deletes them.
