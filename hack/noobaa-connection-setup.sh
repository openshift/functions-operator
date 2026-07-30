#!/usr/bin/env bash

# NooBaa Connection setup with defaults
#
# Prerequisites: OpenShift Data Foundation installed in `openshift-storage`
#
# before running this secript, install the CRD and the objectbucket-notifications-adapter from this repo:
#   make install
#   make deploy IMG=<some-registry>/objectbucket-notifications-adapter:tag

oc create secret generic mcg-adapter-connection \
  --from-file=connect.json=/dev/stdin -n openshift-storage <<EOF
{
  "name": "mcg-adapter-connection",
  "notification_protocol": "http",
  "agent_request_object": {
    "host": "objectbucket-notifications-adapter-notifications.objectbucket-notifications-adapter-system.svc.cluster.local",
    "port": 8888
  } 
}
EOF

existing_connections=$(oc get noobaa noobaa -n openshift-storage -o json | jq -c '.spec.bucketNotifications.connections // []')

updated_connections=$(echo "$existing_connections" | jq -c \
  --arg name "mcg-adapter-connection" \
  '[.[] | select(.name != $name)] + [{"name": $name, "namespace": "openshift-storage"}]')

oc patch noobaa noobaa --type='merge' -n openshift-storage -p '{
  "spec": {
    "bucketNotifications": {
      "connections": '"${updated_connections}"',
      "enabled": true
    }
  }
}'
