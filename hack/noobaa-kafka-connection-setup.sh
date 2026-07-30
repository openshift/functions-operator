#!/usr/bin/env bash

# NooBaa Kafka Connection setup with defaults
#
# Prerequisites:
#   - OpenShift Data Foundation installed in `openshift-storage`
#   - Strimzi Kafka cluster `my-cluster` running in `kafka` namespace
#
# Before running this script, install the CRD and the objectbucket-notifications-adapter from this repo:
#   make install
#   make deploy-kafka IMG=<some-registry>/objectbucket-notifications-adapter:tag

set -Eeuxo pipefail

mcg_adapter_topic="mcg-adapter-notifications"
mcg_adapter_namespace="objectbucket-notifications-adapter-system"
kafka_namespace="kafka"
kafka_cluster="my-cluster"
connection_name="mcg-adapter-connection"

# --- Strimzi KafkaTopic and KafkaUsers ---

cat <<EOF | oc apply -f -
apiVersion: kafka.strimzi.io/v1
kind: KafkaTopic
metadata:
  name: ${mcg_adapter_topic}
  namespace: ${kafka_namespace}
  labels:
    strimzi.io/cluster: ${kafka_cluster}
spec:
  partitions: 8
  replicas: 3
  config:
    retention.ms: 86400000
    cleanup.policy: delete
EOF

cat <<EOF | oc apply -f -
apiVersion: kafka.strimzi.io/v1
kind: KafkaUser
metadata:
  name: noobaa-notifications-user
  namespace: ${kafka_namespace}
  labels:
    strimzi.io/cluster: ${kafka_cluster}
spec:
  authentication:
    type: scram-sha-512
  authorization:
    type: simple
    acls:
      - resource:
          type: topic
          name: "${mcg_adapter_topic}"
          patternType: literal
        operations: [Read, Describe, Write, Create, Delete]
        host: "*"
      - resource:
          type: group
          name: "${mcg_adapter_topic}"
          patternType: literal
        operations: [Describe]
        host: "*"
EOF

cat <<EOF | oc apply -f -
apiVersion: kafka.strimzi.io/v1
kind: KafkaUser
metadata:
  name: objectbucket-notifications-adapter-user
  namespace: ${kafka_namespace}
  labels:
    strimzi.io/cluster: ${kafka_cluster}
spec:
  authentication:
    type: scram-sha-512
  authorization:
    type: simple
    acls:
      - resource:
          type: topic
          name: "*"
        operations: [Read, Describe, Write, Create, Delete]
        host: "*"
      - resource:
          type: group
          name: "*"
        operations: [Read]
        host: "*"
EOF

echo "Waiting for KafkaUser secrets to be created..."
oc wait --for=condition=Ready kafkauser/noobaa-notifications-user -n "${kafka_namespace}" --timeout=120s
oc wait --for=condition=Ready kafkauser/objectbucket-notifications-adapter-user -n "${kafka_namespace}" --timeout=120s

# --- NooBaa Kafka connection secret ---

noobaa_password=$(oc get secret -n "${kafka_namespace}" noobaa-notifications-user \
  -o jsonpath='{.data.password}' | base64 --decode)

tmp=$(mktemp -d)
cat > "$tmp/connect.json" <<EOF
{
  "name": "${connection_name}",
  "notification_protocol": "kafka",
  "topic": "${mcg_adapter_topic}",
  "kafka_options_object": {
    "metadata.broker.list": "${kafka_cluster}-kafka-bootstrap.${kafka_namespace}.svc:9095",
    "security.protocol": "SASL_PLAINTEXT",
    "sasl.mechanism": "SCRAM-SHA-512",
    "sasl.username": "noobaa-notifications-user",
    "sasl.password": "${noobaa_password}"
  }
}
EOF

oc delete secret -n openshift-storage "${connection_name}" 2>/dev/null || true
oc create secret generic "${connection_name}" \
  --from-file="$tmp/connect.json" -n openshift-storage
rm -rf "$tmp"

# --- Patch NooBaa CR ---

existing_connections=$(oc get noobaa noobaa -n openshift-storage -o json \
  | jq -c '.spec.bucketNotifications.connections // []')

updated_connections=$(echo "$existing_connections" | jq -c \
  --arg name "${connection_name}" \
  '[.[] | select(.name != $name)] + [{"name": $name, "namespace": "openshift-storage"}]')

oc patch noobaa noobaa --type='merge' -n openshift-storage -p '{
  "spec": {
    "bucketNotifications": {
      "connections": '"${updated_connections}"',
      "enabled": true
    }
  }
}'

# --- objectbucket-notifications-adapter Kafka credentials secret ---
# Copy the objectbucket-notifications-adapter-user password from the Strimzi-managed secret in the
# kafka namespace into a secret in the adapter namespace, using the format
# expected by the adapter (see kafka-secret-format.md).

oc create namespace "${mcg_adapter_namespace}" 2>/dev/null || true

adapter_password=$(oc get secret -n "${kafka_namespace}" objectbucket-notifications-adapter-user \
  -o jsonpath='{.data.password}' | base64 --decode)

oc delete secret -n "${mcg_adapter_namespace}" objectbucket-notifications-adapter-user 2>/dev/null || true
oc create secret generic objectbucket-notifications-adapter-user -n "${mcg_adapter_namespace}" \
  --from-literal=protocol=SASL_PLAINTEXT \
  --from-literal=sasl.mechanism=SCRAM-SHA-512 \
  --from-literal=user=objectbucket-notifications-adapter-user \
  --from-literal=password="${adapter_password}"
