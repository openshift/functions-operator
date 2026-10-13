#!/usr/bin/env bash
#
# Prepare an existing OpenShift cluster for the functions-operator e2e suite.
# Unlike hack/create-kind-cluster.sh this does NOT create a cluster: it installs
# only the dependencies the suite needs onto the cluster your current `oc` context
# points at.
#
# Installs the full dependency set by default (each gated by an env flag):
#   - Gitea                + the kube-public/gitea-endpoint ConfigMap the suite reads
#   - Red Hat OpenShift Pipelines (productized Tekton) for on-cluster func builds
#   - Custom Metrics Autoscaler (productized KEDA) incl. the HTTP add-on, for the
#     keda deployer
# Opt out of any piece with INSTALL_GITEA=false / INSTALL_PIPELINES=false /
# INSTALL_KEDA=false. Preparing everything means one prep covers both raw and keda.
#
# No container registry is deployed: the suite uses ttl.sh (set REGISTRY=ttl.sh/...)
# together with on-cluster builds (FUNC_REMOTE=true), which produce a ttl.sh-servable
# image. See the e2e run recipe in the repo docs.

set -o errexit
set -o nounset
set -o pipefail

BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFESTS="${BASEDIR}/manifests"

INSTALL_GITEA="${INSTALL_GITEA:-true}"
INSTALL_PIPELINES="${INSTALL_PIPELINES:-true}"
# Prepare the cluster fully by default so it's ready for both the raw and keda
# deployers without re-running prep. CMA (KEDA) is a handful of extra pods and does
# not interfere with raw runs. Opt out with INSTALL_KEDA=false.
INSTALL_KEDA="${INSTALL_KEDA:-true}"

header=$'\e[1;33m'
reset=$'\e[0m'
function header_text { echo "$header$*$reset"; }

# Wait for an OLM subscription's CSV to reach Succeeded.
# args: <subscription-name> <namespace>
function wait_for_csv() {
  local sub="$1" ns="$2" csv=""
  header_text "Waiting for CSV of subscription ${sub} in ${ns}"
  for _ in $(seq 1 60); do
    csv="$(oc get subscription "$sub" -n "$ns" -o jsonpath='{.status.installedCSV}' 2>/dev/null || true)"
    if [ -n "$csv" ] && [ "$(oc get csv "$csv" -n "$ns" -o jsonpath='{.status.phase}' 2>/dev/null || true)" = "Succeeded" ]; then
      header_text "CSV ${csv} ready"
      return 0
    fi
    sleep 10
  done
  echo "Timed out waiting for CSV of ${sub} in ${ns}" >&2
  return 1
}

function install_gitea() {
  header_text "Installing Gitea"
  oc apply -f "${MANIFESTS}/gitea.yaml"
  oc rollout status deploy/gitea -n gitea --timeout=180s

  local host
  host="$(oc get route gitea -n gitea -o jsonpath='{.spec.host}')"

  # The suite (test/utils/gitea.go) discovers Gitea via this ConfigMap. It requires
  # both keys. The SSH endpoint must be reachable from inside the cluster (the
  # operator's syncer and the Tekton git-clone task clone over it; the test host
  # only builds the URL string, it never dials SSH). The Route only serves HTTP,
  # so SSH points at the in-cluster service, not the route host.
  header_text "Writing kube-public/gitea-endpoint ConfigMap (http://${host})"
  oc create configmap gitea-endpoint -n kube-public \
    --from-literal="http=http://${host}" \
    --from-literal="ssh=gitea.gitea.svc:2222" \
    --dry-run=client -o yaml | oc apply -f -
}

function install_pipelines() {
  header_text "Installing Red Hat OpenShift Pipelines"
  oc apply -f "${MANIFESTS}/pipelines.yaml"
  wait_for_csv openshift-pipelines-operator-rh openshift-operators

  header_text "Waiting for TektonConfig to be created"
  for _ in $(seq 1 30); do
    oc get tektonconfig config >/dev/null 2>&1 && break
    sleep 5
  done

  header_text "Waiting for TektonPipeline to be ready"
  oc wait --for=condition=Ready --timeout=300s tektonpipeline/pipeline || true

  # Disable Tekton Results. Productized OpenShift Pipelines enables it by default; its
  # results.tekton.dev/taskrun finalizer races with test-namespace deletion and hangs
  # cleanup when the namespace is deleted before the Results watcher clears the finalizer.
  # We don't need run history for e2e, so turn it off for deterministic teardown.
  # (Field is singular "result"; plural "results" is silently ignored.)
  header_text "Disabling Tekton Results"
  oc patch tektonconfig config --type=merge -p '{"spec":{"result":{"disabled":true}}}'
}

function install_keda() {
  header_text "Installing Custom Metrics Autoscaler (productized KEDA)"
  oc apply -f "${MANIFESTS}/cma.yaml"
  wait_for_csv openshift-custom-metrics-autoscaler-operator openshift-keda

  # The operator auto-creates a KedaController named "keda"; wait for it, then enable
  # the HTTP add-on, which func's keda deployer needs (HTTPScaledObject). Off by default.
  header_text "Enabling the KEDA HTTP add-on"
  for _ in $(seq 1 30); do
    oc get kedacontroller keda -n openshift-keda >/dev/null 2>&1 && break
    sleep 5
  done
  oc patch kedacontroller keda -n openshift-keda --type=merge \
    -p '{"spec":{"watchNamespace":"","httpAddon":{"enabled":true}}}'

  header_text "Waiting for KEDA HTTP add-on pods"
  oc rollout status deploy/keda-add-ons-http-interceptor -n openshift-keda --timeout=180s || true
  oc rollout status deploy/keda-add-ons-http-scaler -n openshift-keda --timeout=180s || true
}

[ "$INSTALL_GITEA" = "true" ]     && install_gitea
[ "$INSTALL_PIPELINES" = "true" ] && install_pipelines
[ "$INSTALL_KEDA" = "true" ]      && install_keda

header_text "Cluster prepared for functions-operator e2e"
