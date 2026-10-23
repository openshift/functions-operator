#!/usr/bin/env bash
#
# Prepare an existing OpenShift cluster for the functions-operator e2e suite:
# installs the dependencies (Gitea, Red Hat OpenShift Pipelines, Custom Metrics
# Autoscaler) onto the cluster the current `oc` context points at.
#
# Each piece is gated by INSTALL_GITEA / INSTALL_PIPELINES / INSTALL_KEDA
# (default true, so one prep covers both the raw and keda deployers).
#
# No registry is deployed: the suite pushes to ttl.sh (REGISTRY=ttl.sh/...)
# via on-cluster builds (FUNC_REMOTE=true). See the e2e run recipe in the docs.

set -o errexit
set -o nounset
set -o pipefail

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS="${BASEDIR}/manifests"

INSTALL_GITEA="${INSTALL_GITEA:-true}"
INSTALL_PIPELINES="${INSTALL_PIPELINES:-true}"
INSTALL_KEDA="${INSTALL_KEDA:-true}"

# Retry a command until it succeeds or the attempts run out.
# A freshly installed operator may not have its CRs or webhook serving yet.
# args: <attempts> <delay-seconds> <what is waited for> <command...>
function retry() {
  local attempts="$1" delay="$2" what="$3"; shift 3
  echo "Waiting for ${what}"
  for _ in $(seq 1 "$attempts"); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep "$delay"
  done
  echo "Timed out waiting for ${what}" >&2
  return 1
}

# True once an OLM subscription's installed CSV has reached Succeeded.
# args: <subscription-name> <namespace>
function csv_succeeded() {
  local csv
  csv="$(oc get subscription "$1" -n "$2" -o jsonpath='{.status.installedCSV}' 2>/dev/null)" || return 1
  [ -n "$csv" ] \
    && [ "$(oc get csv "$csv" -n "$2" -o jsonpath='{.status.phase}' 2>/dev/null)" = "Succeeded" ]
}

function install_gitea() {
  oc apply -f "${MANIFESTS}/gitea.yaml"
  oc rollout status deploy/gitea -n gitea --timeout=180s

  # The suite (test/utils/gitea.go) discovers Gitea via this ConfigMap. SSH
  # points at the in-cluster service, not the Route: the Route serves HTTP only.
  local host
  host="$(oc get route gitea -n gitea -o jsonpath='{.spec.host}')"
  oc create configmap gitea-endpoint -n kube-public \
    --from-literal="http=http://${host}" \
    --from-literal="ssh=gitea.gitea.svc:2222" \
    --dry-run=client -o yaml | oc apply -f -
}

function install_pipelines() {
  oc apply -f "${MANIFESTS}/pipelines.yaml"
  retry 60 10 "Pipelines operator CSV to succeed" \
    csv_succeeded openshift-pipelines-operator-rh openshift-operators
  retry 30 5 "TektonPipeline to be ready" \
    oc wait --for=condition=Ready --timeout=30s tektonpipeline/pipeline

  # Disable Tekton Results: its TaskRun finalizer races namespace deletion and
  # hangs suite cleanup. (Field is singular "result"; "results" is ignored.)
  # Runs once: the Ready wait above already proves the operator is serving.
  oc patch tektonconfig config --type=merge -p '{"spec":{"result":{"disabled":true}}}'
}

function install_keda() {
  oc apply -f "${MANIFESTS}/cma.yaml"
  retry 60 10 "CMA operator CSV to succeed" \
    csv_succeeded openshift-custom-metrics-autoscaler-operator openshift-keda

  # The operator auto-creates the KedaController named "keda". Wait for it and
  # for the admission webhook to serve before patching it.
  retry 30 5 "KedaController to be created" oc get kedacontroller keda -n openshift-keda
  retry 30 10 "KEDA admission webhook to roll out" \
    oc rollout status deploy/keda-admission -n openshift-keda --timeout=60s

  # The keda deployer needs the HTTP add-on (HTTPScaledObject); off by default.
  oc patch kedacontroller keda -n openshift-keda --type=merge \
    -p '{"spec":{"watchNamespace":"","httpAddon":{"enabled":true}}}'

  retry 12 10 "KEDA HTTP interceptor to roll out" \
    oc rollout status deploy/keda-add-ons-http-interceptor -n openshift-keda --timeout=120s
  retry 12 10 "KEDA HTTP scaler to roll out" \
    oc rollout status deploy/keda-add-ons-http-scaler -n openshift-keda --timeout=120s
}

[ "$INSTALL_GITEA" = "true" ]     && install_gitea
[ "$INSTALL_PIPELINES" = "true" ] && install_pipelines
[ "$INSTALL_KEDA" = "true" ]      && install_keda

echo "Cluster prepared for functions-operator e2e"
