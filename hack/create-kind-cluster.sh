#!/bin/bash

set -e
set -o errexit
set -o nounset
set -o pipefail

# Default configuration
DELETE_CLUSTER_BEFORE=true
CLUSTER_NAME=${CLUSTER_NAME:-kind}
NODE_VERSION="v1.34.0"
REGISTRY_NAME="kind-registry"
REGISTRY_PORT=${REGISTRY_PORT:-"5001"}

SERVING_VERSION="v1.21.0"
TEKTON_VERSION="v1.11.0"
KEDA_VERSION="v2.19.0"
KEDA_HTTP_ADDON_VERSION="v0.13.0"

CERT_MANAGER_VERSION="v1.20.2"
TRUST_MANAGER_VERSION="v0.22.0"

GITEA_USER="giteaadmin"
GITEA_PASS="giteapass"

header=$'\e[1;33m'
reset=$'\e[0m'

function header_text {
	echo "$header$*$reset"
}

function kubectl_apply_with_retry() {
  local max_attempts=5
  local delay=5
  local attempt

  for attempt in $(seq 1 $max_attempts); do
    if kubectl apply "$@"; then
      return 0
    fi

    if [ "$attempt" -lt "$max_attempts" ]; then
      header_text "kubectl apply failed (attempt $attempt/$max_attempts), retrying in ${delay}s..."
      sleep "$delay"
      delay=$((delay * 2))
    fi
  done

  header_text "kubectl apply failed after $max_attempts attempts"
  return 1
}

function delete_existing_cluster() {
  header_text "Deleting existing Kind cluster..."
  kind delete cluster --name "$CLUSTER_NAME" || true
}

function setup_local_registry() {
  if [ "$(docker inspect -f '{{.State.Running}}' "${REGISTRY_NAME}" 2>/dev/null || true)" == "true" ]; then
    reg_port="$(docker inspect -f '{{ (index (index .NetworkSettings.Ports "5000/tcp") 0).HostPort}}' "${REGISTRY_NAME}" 2>/dev/null)"
    if [ "${reg_port}" != "${REGISTRY_PORT}" ]; then
      header_text "existing registry is running on another port (${reg_port}). Cleaning it up..."
      docker stop "${REGISTRY_NAME}"
      docker rm "${REGISTRY_NAME}"
    fi
  fi

  if [ "$(docker inspect -f '{{.State.Running}}' "${REGISTRY_NAME}" 2>/dev/null || true)" != 'true' ]; then
    header_text "create registry container for port ${REGISTRY_PORT} with HTTPS"

    # Create persistent directory for registry certs in project
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    REGISTRY_CERTS_DIR="${SCRIPT_DIR}/registry-certs"
    mkdir -p "${REGISTRY_CERTS_DIR}"

    # Generate self-signed certificate if it doesn't exist
    if [ ! -f "${REGISTRY_CERTS_DIR}/registry.crt" ]; then
      header_text "Generating self-signed certificate for registry"
      openssl req -newkey rsa:4096 -nodes -sha256 \
        -keyout "${REGISTRY_CERTS_DIR}/registry.key" \
        -x509 -days 365 -out "${REGISTRY_CERTS_DIR}/registry.crt" \
        -subj "/CN=${REGISTRY_NAME}" \
        -addext "subjectAltName=DNS:kind-registry,DNS:localhost,IP:127.0.0.1"
    fi

    # Run registry with HTTPS
    docker run -d --restart=always \
      -p "127.0.0.1:${REGISTRY_PORT}:5000" \
      -v "${REGISTRY_CERTS_DIR}:/certs" \
      -e REGISTRY_HTTP_TLS_CERTIFICATE=/certs/registry.crt \
      -e REGISTRY_HTTP_TLS_KEY=/certs/registry.key \
      --name "${REGISTRY_NAME}" \
      docker.io/registry:2
  fi
}

function create_kind_cluster() {
  header_text "Creating Kind cluster '$CLUSTER_NAME'..."
  cat <<EOF | kind create cluster --name "$CLUSTER_NAME" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  image: kindest/node:$NODE_VERSION
- role: worker
  image: kindest/node:$NODE_VERSION
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:$REGISTRY_PORT"]
    endpoint = ["https://$REGISTRY_NAME:5000"]
EOF
}

function connect_registry_to_cluster() {
  if [ "$(docker inspect -f='{{json .NetworkSettings.Networks.kind}}' "${REGISTRY_NAME}")" = 'null' ]; then
    header_text "connect the registry to the cluster network"
    docker network connect "kind" "${REGISTRY_NAME}"
  fi

  # Document the local registry
  kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: local-registry-hosting
  namespace: kube-public
data:
  localRegistryHosting.v1: |
    host: "localhost:$REGISTRY_PORT"
    help: "https://kind.sigs.k8s.io/docs/user/local-registry/"
EOF

  # Install registry certificate on all nodes
  header_text "Installing registry certificate on cluster nodes"
  for node in $(kind get nodes --name "$CLUSTER_NAME"); do
    docker exec "$REGISTRY_NAME" cat /certs/registry.crt | docker exec -i "$node" bash -c 'mkdir -p /usr/local/share/ca-certificates && cat > /usr/local/share/ca-certificates/kind-registry.crt && update-ca-certificates'
    docker exec "$node" systemctl restart containerd
  done
}

function install_tekton() {
  header_text "Install Tekton"
  kubectl_apply_with_retry -f https://infra.tekton.dev/tekton-releases/pipeline/previous/${TEKTON_VERSION}/release.yaml
  kubectl patch configmap feature-flags -n tekton-pipelines --type merge -p '{"data":{"coschedule":"disabled"}}'

  header_text "Waiting for Tekton to be ready..."
  kubectl wait deployment --all --timeout=-1s --for=condition=Available -n tekton-pipelines
  kubectl wait deployment --all --timeout=-1s --for=condition=Available -n tekton-pipelines-resolvers
}

function install_knative_serving() {
  header_text "Installing Knative Serving..."
  kubectl_apply_with_retry -f https://github.com/knative/serving/releases/download/knative-${SERVING_VERSION}/serving-crds.yaml
  kubectl_apply_with_retry -f https://github.com/knative/serving/releases/download/knative-${SERVING_VERSION}/serving-core.yaml
  kubectl_apply_with_retry -f https://github.com/knative/net-kourier/releases/download/knative-${SERVING_VERSION}/kourier.yaml

  kubectl patch configmap/config-network \
    --namespace knative-serving \
    --type merge \
    --patch '{"data":{"ingress-class":"kourier.ingress.networking.knative.dev"}}'

  header_text "Waiting for Knative Serving to be ready..."
  kubectl wait deployment --all --timeout=-1s --for=condition=Available -n knative-serving
  kubectl wait deployment --all --timeout=-1s --for=condition=Available -n kourier-system
}

function install_keda() {
  header_text "Installing keda"
  kubectl_apply_with_retry --server-side -f https://github.com/kedacore/keda/releases/download/${KEDA_VERSION}/keda-${KEDA_VERSION:1}.yaml
  kubectl_apply_with_retry --server-side -f https://github.com/kedacore/keda/releases/download/${KEDA_VERSION}/keda-${KEDA_VERSION:1}-core.yaml
  header_text "Waiting for Keda to become ready"
  kubectl wait deployment --all --timeout=-1s --for=condition=Available --namespace keda

  header_text "Installing keda HTTP add-on"
  kubectl_apply_with_retry --server-side -f https://github.com/kedacore/http-add-on/releases/download/${KEDA_HTTP_ADDON_VERSION}/keda-add-ons-http-${KEDA_HTTP_ADDON_VERSION:1}-crds.yaml
  kubectl_apply_with_retry --server-side -f https://github.com/kedacore/http-add-on/releases/download/${KEDA_HTTP_ADDON_VERSION}/keda-add-ons-http-${KEDA_HTTP_ADDON_VERSION:1}.yaml
  header_text "Waiting for Keda HTTP add-on to become ready"
  kubectl wait deployment --all --timeout=-1s --for=condition=Available --namespace keda
}

function install_gitea() {
  header_text "Installing Gitea"

  helm repo add gitea-charts https://dl.gitea.com/charts/
  helm repo update
  helm install gitea gitea-charts/gitea --namespace gitea --create-namespace \
    --set service.http.type=NodePort \
    --set service.http.nodePort=30000 \
    --set service.ssh.type=NodePort \
    --set service.ssh.nodePort=30022 \
    --set gitea.admin.username="${GITEA_USER}" \
    --set gitea.admin.password="${GITEA_PASS}" \
    --set gitea.admin.email=admin@gitea.local \
    --set persistence.enabled=false \
    --set postgresql-ha.enabled=false \
    --set postgresql.enabled=true \
    --set postgresql.persistence.enabled=false \
    --set redis-cluster.enabled=false \
    --set redis.enabled=false

  header_text "Waiting for Gitea to become ready"
  kubectl wait deployment --all --timeout=-1s --for=condition=Available --namespace gitea

  # Get Gitea endpoint for tests
  GITEA_NODE_IP=$(docker inspect kind-control-plane --format '{{.NetworkSettings.Networks.kind.IPAddress}}')

  # Create ConfigMap with Gitea endpoint info
  kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: gitea-endpoint
  namespace: kube-public
data:
  http: "http://${GITEA_NODE_IP}:30000"
  ssh: "${GITEA_NODE_IP}:30022"
EOF
}

if [ "$DELETE_CLUSTER_BEFORE" = "true" ]; then
  delete_existing_cluster
fi

setup_local_registry
create_kind_cluster
connect_registry_to_cluster
install_tekton
install_knative_serving
install_keda
install_gitea

header_text "All components installed"
