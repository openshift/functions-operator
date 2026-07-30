#!/usr/bin/env bash

# Ceph/Rook Kafka Connection setup with defaults
#
# Prerequisites:
#   - OpenShift Data Foundation installed in `openshift-storage`
#   - Strimzi Kafka cluster `my-cluster` running in `kafka` namespace
#
# Before running this script, install the CRD and the objectbucket-notifications-adapter from this repo:
#   make install
#   make deploy-kafka IMG=<some-registry>/objectbucket-notifications-adapter:tag

set -Eeuxo pipefail

rgw_adapter_topic="rgw-adapter-notifications"
rgw_adapter_namespace="objectbucket-notifications-adapter-system"
kafka_namespace="kafka"
kafka_cluster="my-cluster"
notification_name="objectbucket-notifications-adapter"
notification_kafka_user="rgw-objectbucket-notifications-adapter"

# --- Strimzi KafkaTopic and KafkaUsers ---

cat <<EOF | oc apply -f -
apiVersion: kafka.strimzi.io/v1
kind: KafkaTopic
metadata:
  name: ${rgw_adapter_topic}
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

# TODO: cannot use SCRAM-SHA-512 with Kafka >= 4.0.0 because of https://github.com/confluentinc/librdkafka/pull/4895 , rhel9 image has librdkafka-1.6.1-102.el9.x86_64 , which doesn't have the fix
#cat <<EOF | oc apply -f -
#apiVersion: kafka.strimzi.io/v1
#kind: KafkaUser
#metadata:
#  name: ${notification_kafka_user}
#  namespace: ${kafka_namespace}
#  labels:
#    strimzi.io/cluster: ${kafka_cluster}
#spec:
#  authentication:
#    type: scram-sha-512
#  authorization:
#    type: simple
#    acls:
#      - resource:
#          type: topic
#          name: "${rgw_adapter_topic}"
#          patternType: literal
#        operations: [Read, Describe, Write, Create, Delete]
#        host: "*"
#      - resource:
#          type: group
#          name: "${rgw_adapter_topic}"
#          patternType: literal
#        operations: [Describe]
#        host: "*"
#EOF

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
#oc wait --for=condition=Ready kafkauser/"${notification_kafka_user}" -n "${kafka_namespace}" --timeout=120s
oc wait --for=condition=Ready kafkauser/objectbucket-notifications-adapter-user -n "${kafka_namespace}" --timeout=120s

# --- Rook->Kafka connection secret ---
# strimzi_crt=$(oc -n "${kafka_namespace}" get secret my-cluster-cluster-ca-cert --template='{{index .data "ca.crt"}}' | base64 --decode )

#rgw_kafka_cacrt=$(oc -n "${kafka_namespace}" get secret "${notification_kafka_user}" --template='{{index .data "ca.crt"}}' | base64 --decode )
#rgw_kafka_usercrt=$(oc -n "${kafka_namespace}" get secret "${notification_kafka_user}" --template='{{index .data "user.crt"}}' | base64 --decode )
#rgw_kafka_userkey=$(oc -n "${kafka_namespace}" get secret "${notification_kafka_user}" --template='{{index .data "user.key"}}' | base64 --decode )

#rgw_kafka_username="${notification_kafka_user}"
#rgw_kafka_password=$(oc -n "${kafka_namespace}" get secret "${notification_kafka_user}" --template='{{index .data "password"}}' | base64 --decode )

# CA secret
#oc delete secret --namespace openshift-storage "${notification_kafka_user}-kafka" 2>/dev/null || true
#oc create secret --namespace openshift-storage generic "${notification_kafka_user}-kafka" \
#      --from-literal=ca.crt="${rgw_kafka_cacrt}" \
#      --from-literal=user.crt="${rgw_kafka_usercrt}" \
#      --from-literal=user.key="${rgw_kafka_userkey}"

# Patch the cephObjectStores reconcileStrategy to Init so that it allows us adding the additionalVolumeMounts
#oc patch storagecluster ocs-storagecluster -n openshift-storage \
#  --type=merge \
#  -p '{"spec":{"managedResources":{"cephObjectStores":{"reconcileStrategy":"init"}}}}'

#echo "Sleeping for 10s to let the StorageCluster update the reconcileStrategy"
#sleep 10

# Mount the Kafka Secret
#cat <<EOF | oc apply -f -
#apiVersion: ceph.rook.io/v1
#kind: CephObjectStore
#metadata:
#  name: ocs-storagecluster-cephobjectstore
#  namespace: openshift-storage
#spec:
#  gateway:
#    additionalVolumeMounts:
#      - subPath: ${notification_kafka_user}-kafka
#        volumeSource:
#          secret:
#            secretName: ${notification_kafka_user}-kafka
#            defaultMode: 0644
#EOF

#oc delete secret --namespace openshift-storage "${notification_kafka_user}" 2>/dev/null || true
#oc create secret --namespace openshift-storage generic "${notification_kafka_user}" \
#      --from-literal=user="${notification_kafka_user}" \
#      --from-literal=password="${rgw_kafka_password}"

# Create a CephObjectStoreUser so we have credentials for the CreateTopic command
cat <<EOF | oc apply -f -
apiVersion: ceph.rook.io/v1
kind: CephObjectStoreUser
metadata:
  name: objectbucket-notifications-adapter-topic-admin
  namespace: openshift-storage
spec:
  store: ocs-storagecluster-cephobjectstore
  displayName: "ObjectBucket Notification Adapter Topic Admin"
EOF

oc wait --for=jsonpath='{.status.phase}'=Ready cephobjectstoreuser objectbucket-notifications-adapter-topic-admin -n openshift-storage --timeout=1m
topicAdminSecretName=$(oc get cephobjectstoreuser objectbucket-notifications-adapter-topic-admin -n openshift-storage -o json | jq -r '.status.info.secretName')
ROOK_ACCESS_KEY=$(oc extract secret/$topicAdminSecretName -n openshift-storage --keys=AccessKey --to=- 2>/dev/null)
ROOK_SECRET_KEY=$(oc extract secret/$topicAdminSecretName -n openshift-storage --keys=SecretKey --to=- 2>/dev/null)

S3_ENDPOINT=https://$(oc get route ocs-storagecluster-cephobjectstore-secure -n openshift-storage -o json | jq -r ".spec.host")
aws_alias() {
  AWS_ACCESS_KEY_ID=$ROOK_ACCESS_KEY AWS_SECRET_ACCESS_KEY=$ROOK_SECRET_KEY aws --endpoint "$S3_ENDPOINT" --no-verify-ssl "$@"
}

# TODO: cannot use SCRAM-SHA-512 because of https://github.com/confluentinc/librdkafka/pull/4895 , rhel9 image has librdkafka-1.6.1-102.el9.x86_64 , which doesn't have the fix
#aws_alias sns create-topic \
#  --region default \
#  --name "${rgw_adapter_topic}" \
#  --attributes '{
#    "push-endpoint": "kafka://'${notification_kafka_user}:${rgw_kafka_password}@${kafka_cluster}'-kafka-bootstrap.'${kafka_namespace}.svc:9094'",
#    "use-ssl": "true",
#    "ca-location": "/var/rgw/kafka-ca/ca.crt",
#    "mechanism": "SCRAM-SHA-512",
#    "kafka-ack-level": "broker"
#}'

# TODO: Cannot use tls either, as ceph v20.1.0 does not yet implement mTLS (user cert/key)
#aws_alias sns create-topic \
#  --region default \
#  --name "${rgw_adapter_topic}" \
#  --attributes '{
#    "push-endpoint": "kafka://'${kafka_cluster}'-kafka-bootstrap.'${kafka_namespace}.svc:9093'",
#    "use-ssl": "true",
#    "ca-location": "/var/rgw/'${notification_kafka_user}-kafka'/ca.crt",
#    "kafka-ack-level": "broker"
#}'

# TODO: So instead relying on an anonymous admin user
aws_alias sns create-topic \
  --region default \
  --name "${rgw_adapter_topic}" \
  --attributes '{
    "push-endpoint": "kafka://'${kafka_cluster}'-kafka-bootstrap.'${kafka_namespace}.svc:9092'",
    "use-ssl": "false",
    "kafka-ack-level": "broker"
}'

#aws_alias sns create-topic \
#  --region default \
#  --name "${rgw_adapter_topic}" \
#  --attributes '{
#    "push-endpoint": "kafka://'${kafka_cluster}'-kafka-bootstrap.'${kafka_namespace}.svc:9092'",
#    "use-ssl": "false",
#    "kafka-ack-level": "broker"
#}'

#cat <<EOF | oc apply -f -
#apiVersion: ceph.rook.io/v1
#kind: CephBucketTopic
#metadata:
#  name: ${rgw_adapter_topic}
#  namespace: openshift-storage
#spec:
#  objectStoreName: ocs-storagecluster-cephobjectstore
#  objectStoreNamespace: openshift-storage
#  persistent: true
#  endpoint:
#    kafka:
#      uri: "kafka://${kafka_cluster}-kafka-bootstrap.${kafka_namespace}.svc:9094"
#      mechanism: SCRAM-SHA-512
#      ackLevel: broker
#      useSSL: true
#      caLocation: /var/rgw/kafka-ca/ca.crt
#      userSecretRef:
#        name: ${notification_kafka_user}
#        key: user
#      passwordSecretRef:
#        name: ${notification_kafka_user}
#        key: password
#EOF


# --- objectbucket-notifications-adapter Kafka credentials secret ---
# Copy the objectbucket-notifications-adapter-user password from the Strimzi-managed secret in the
# kafka namespace into a secret in the adapter namespace, using the format
# expected by the adapter (see kafka-secret-format.md).

oc create namespace "${rgw_adapter_namespace}" 2>/dev/null || true

adapter_password=$(oc get secret -n "${kafka_namespace}" objectbucket-notifications-adapter-user \
  -o jsonpath='{.data.password}' | base64 --decode)

oc delete secret -n "${rgw_adapter_namespace}" objectbucket-notifications-adapter-user 2>/dev/null || true
oc create secret generic objectbucket-notifications-adapter-user -n "${rgw_adapter_namespace}" \
  --from-literal=protocol=SASL_PLAINTEXT \
  --from-literal=sasl.mechanism=SCRAM-SHA-512 \
  --from-literal=user=objectbucket-notifications-adapter-user \
  --from-literal=password="${adapter_password}"


