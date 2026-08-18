# ObjectBucket Notifications Adapter Configuration

The `objectbucket-notifications-adapter` supports runtime configuration through a Kubernetes ConfigMap. This allows you to modify adapter settings without restarting the pod.

## Configuration Overview

The adapter uses two types of configuration:

### Static Configuration (Command-line flags)

These settings are provided at pod startup and require a pod restart to change:

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `objectbucket-notifications-adapter-config` | Name of the ConfigMap containing adapter configuration |
| `--create-config` | `false` | If set, create the default configuration ConfigMap at startup when it does not already exist (requires `create` permission on ConfigMaps) |
| `--adapter-port` | `8888` | Port the notification HTTP server listens on (HTTP mode only) |
| `--notifications-mode` | `http` | Default for the `NOTIFICATIONS_MODE` ConfigMap key (see below) |
| `--kafka-brokers` | _(none)_ | Default for the `KAFKA_BROKERS` ConfigMap key (see below) |
| `--kafka-notifications-topics` | _(none)_ | Default for the `KAFKA_NOTIFICATIONS_TOPICS` ConfigMap key (see below) |
| `--kafka-notifications-group-id` | _(none)_ | Default for the `KAFKA_NOTIFICATIONS_GROUP_ID` ConfigMap key (see below) |

> **Note:** The notification transport settings (`--notifications-mode`, `--kafka-brokers`,
> `--kafka-notifications-topics`, `--kafka-notifications-group-id`) are now **dynamic**. The
> command-line flags only supply the *defaults*; the effective values come from the ConfigMap
> and can be changed at runtime. When any Kafka setting changes, the adapter gracefully
> restarts its Kafka consumer with the new settings.

### Dynamic Configuration (ConfigMap)

These settings can be changed at runtime by modifying the ConfigMap. The adapter watches for changes and reloads automatically:

| ConfigMap Key | Default | Description |
|---------------|---------|-------------|
| `NOOBAA_ADAPTER_ID` | `mcg-adapter` | Identifier used in the S3 bucket notification configuration for NooBaa-managed OBCs |
| `NOOBAA_ADAPTER_TOPIC_ARN` | `mcg-adapter-connection/connect.json` | NooBaa connection secret reference used as TopicArn in put-bucket-notification calls |
| `NOOBAA_ADAPTER_STORAGECLASS_PATTERN` | `.*noobaa\.io$` | Regex matched against OBC `spec.storageClassName` to classify as NooBaa-managed |
| `RADOSGW_ADAPTER_ID` | `rgw-adapter` | Identifier used in the S3 bucket notification configuration for RadosGW-managed OBCs |
| `RADOSGW_ADAPTER_TOPIC_ARN` | `arn:aws:sns:ocs-storagecluster-cephobjectstore::rgw-adapter-notifications` | RadosGW SNS TopicArn used in put-bucket-notification calls |
| `RADOSGW_ADAPTER_STORAGECLASS_PATTERN` | `.*ceph-rgw$` | Regex matched against OBC `spec.storageClassName` to classify as RadosGW-managed |
| `NOTIFICATIONS_MODE` | value of `--notifications-mode` (`http`) | `http` or `kafka` — selects how the adapter receives NooBaa/RadosGW notifications. Switching modes restarts the notification runner. |
| `KAFKA_BROKERS` | value of `--kafka-brokers` | Comma-separated list of Kafka broker addresses (required for Kafka mode). Changing it gracefully restarts the Kafka consumer. |
| `KAFKA_NOTIFICATIONS_TOPICS` | value of `--kafka-notifications-topics` | Comma-separated list of Kafka topics to consume notifications from (required for Kafka mode). Changing it gracefully restarts the Kafka consumer. |
| `KAFKA_NOTIFICATIONS_GROUP_ID` | value of `--kafka-notifications-group-id` | Consumer group ID for consuming notifications (required for Kafka mode). Changing it gracefully restarts the Kafka consumer. |
| `KAFKA_SECRET` | _(none)_ | Name of a Kubernetes Secret (in the adapter's namespace) containing Kafka credentials. See `kafka-secret-format.md`. |

## Example ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: objectbucket-notifications-adapter-config
  namespace: your-namespace
data:
  # NooBaa adapter configuration
  NOOBAA_ADAPTER_ID: "mcg-adapter"
  NOOBAA_ADAPTER_TOPIC_ARN: "mcg-adapter-connection/connect.json"
  NOOBAA_ADAPTER_STORAGECLASS_PATTERN: ".*noobaa\\.io$"

  # RadosGW adapter configuration
  RADOSGW_ADAPTER_ID: "rgw-adapter"
  RADOSGW_ADAPTER_TOPIC_ARN: "arn:aws:sns:ocs-storagecluster-cephobjectstore::rgw-adapter-notifications"
  RADOSGW_ADAPTER_STORAGECLASS_PATTERN: ".*ceph-rgw$"

  # Optional: Kafka secret for authentication
  KAFKA_SECRET: "kafka-credentials"
```

## Deployment

### 1. Create the ConfigMap

Create the ConfigMap in the same namespace as the adapter:

```bash
kubectl apply -f config/samples/objectbucket-notifications-adapter-config.yaml -n your-namespace
```

### 2. Deploy the Adapter

When deploying the adapter, specify the ConfigMap name and static configuration:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: objectbucket-notifications-adapter
spec:
  template:
    spec:
      containers:
      - name: adapter
        image: your-registry/objectbucket-notifications-adapter:latest
        args:
        - --config=objectbucket-notifications-adapter-config
        - --adapter-port=8888
        - --notifications-mode=http
        # For Kafka mode, add:
        # - --notifications-mode=kafka
        # - --kafka-brokers=broker1:9092,broker2:9092
        # - --kafka-notifications-topics=mcg-notifications,rgw-notifications
        # - --kafka-notifications-group-id=adapter-consumer-group
        env:
        - name: POD_NAMESPACE
          valueFrom:
            fieldRef:
              fieldPath: metadata.namespace
```

## Runtime Configuration Updates

To update the adapter configuration at runtime:

1. Edit the ConfigMap:
   ```bash
   kubectl edit configmap objectbucket-notifications-adapter-config -n your-namespace
   ```

2. The adapter will detect the change and reload the configuration automatically. You'll see log messages like:
   ```
   ConfigMap changed, reloading configuration
   configuration reloaded successfully
   ```

3. The new configuration applies immediately to new reconciliation loops. Existing ObjectBucketSource resources will use the updated configuration on their next reconciliation.

## Dynamic Notification Transport

The notification transport settings (`NOTIFICATIONS_MODE`, `KAFKA_BROKERS`,
`KAFKA_NOTIFICATIONS_TOPICS`, `KAFKA_NOTIFICATIONS_GROUP_ID`) can be changed at runtime
via the ConfigMap. The same applies to the Kafka credentials referenced by `KAFKA_SECRET`.
When any of them change, the adapter:

1. Stops the current notification runner (the HTTP server or the Kafka consumer), waiting
   for it to shut down gracefully.
2. Starts a new runner using the updated settings/credentials.

This means you can, for example, switch the adapter from `http` to `kafka` mode, point the
consumer at different brokers, subscribe to different topics, change the consumer group ID,
or rotate the Kafka credentials — all without restarting the pod.

The runner is restarted only when a change actually affects it: changes to unrelated
ConfigMap keys (e.g. adapter IDs) do not restart it, and a Kafka credential change only
restarts the runner when Kafka is actually in use (`kafka` mode, or `http` mode with
`KAFKA_BROKERS` set for `kafka:` sinks).

If a ConfigMap change produces an invalid notification configuration (for example
`NOTIFICATIONS_MODE=kafka` without `KAFKA_BROKERS`), the reload is rejected: the adapter
logs an error and keeps running with the previous valid configuration.

## Configuration Validation

The adapter validates the configuration when loading:

- Storage class patterns must be valid regular expressions
- All required fields have sensible defaults
- If `KAFKA_SECRET` is specified, the secret must exist in the adapter's namespace

If validation fails, the adapter logs an error and continues using the previous valid configuration.

## Kafka Credential Rotation

To rotate Kafka credentials without restarting the adapter pod:

1. Update the Kafka Secret with new credentials in place, **or** update the ConfigMap to
   reference a new secret via `KAFKA_SECRET`.
2. The adapter watches both the ConfigMap and the referenced Kafka Secret, so it detects the
   change automatically, rebuilds the Kafka configuration, and — if Kafka is in use —
   gracefully restarts its Kafka producer/consumer so the new credentials take effect
   immediately.

Note: The adapter watches the Secret named by the current `KAFKA_SECRET`. If you point
`KAFKA_SECRET` at a different Secret, the watcher automatically follows the new reference.

## Migration from Environment Variables

If you're migrating from the old environment variable configuration:

1. Create a ConfigMap with values from your current environment variables
2. Update your deployment to use command-line flags instead of environment variables for static settings
3. Remove the environment variable definitions from your deployment
4. Dynamic settings (adapter IDs, topics, patterns) can now be updated via the ConfigMap

Example migration:

**Old (env vars):**
```yaml
env:
- name: NOOBAA_ADAPTER_ID
  value: "mcg-adapter"
- name: ADAPTER_PORT
  value: "8888"
```

**New (ConfigMap + flags):**
```yaml
args:
- --adapter-port=8888
- --config=objectbucket-notifications-adapter-config
```

And create a ConfigMap with `NOOBAA_ADAPTER_ID: "mcg-adapter"`.
