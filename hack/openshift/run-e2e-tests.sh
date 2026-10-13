#!/usr/bin/env bash
#
# Run the functions-operator e2e suite against the current OpenShift cluster
# using ttl.sh as the image registry, with all builds and pushes on-cluster.
#
# Prerequisites (see prepare-ocp-cluster.sh in this directory):
#   - operator deployed into func-operator-system (make deploy KUBECTL=oc IMG=...)
#   - Gitea + the kube-public/gitea-endpoint ConfigMap
#   - Red Hat OpenShift Pipelines (the on-cluster builder)
#   - Custom Metrics Autoscaler when DEFAULT_DEPLOYER=keda
#
# Why ttl.sh works only with FUNC_REMOTE=true: the local func pusher wraps
# every pushed image in a single-entry manifest list and re-points the tag at
# it; ttl.sh then fails to serve the per-arch child manifest ("manifest
# unknown"). On-cluster (Tekton/buildah) pushes are plain single manifests,
# which ttl.sh serves fine.
#
# Why FUNC_MANAGEMENT_DISABLED=true: every e2e spec creates and asserts on its
# own Function CR. Without this, each seed `func deploy` also syncs a second CR
# that the operator reconciles out of the spec's control, racing it.
#
# Both variables are read natively by the func CLI (viper FUNC_<flag> env
# binding) and inherited by every func invocation the suite makes.
#
# Environment (all optional, defaults shown):
#   DEFAULT_DEPLOYER   raw | keda (keda needs the CMA installed)
#   E2E_SKIP           regex of spec texts to skip. The default drops the specs
#                      that cannot work under FUNC_REMOTE=true:
#                        SSH - the SSH-repository specs need an SSH endpoint
#                              route the cluster may not expose
#                        Middleware Update, registry auth secret - both seed
#                              with the old func CLI and rely on that seed
#                              carrying stale middleware. Remotely, the old
#                              CLI's pipeline scaffolds with whatever the
#                              mutable upstream ghcr.io/knative/func-utils:v2
#                              tag currently serves, so the seed is never
#                              stale and the specs' premise breaks.
#                        monorepo - the seed's remote deploy does not leave
#                              the function name in the committed func.yaml
#                              the way a local deploy does, so the operator's
#                              metadata read comes back with an empty name and
#                              every reconcile dies at `func describe -n <ns>`
#                              with "must also specify a name when specifying
#                              namespace" (Deployed/Ready never become True).
#   STALE_FUNCTION_IMAGE  pre-built stale-middleware image for the
#                      middleware-rebuild spec; default unset => spec skips
#                      itself. NOTE: with FUNC_REMOTE=true the spec's digested
#                      seed deploy cannot go remote (func only skips build/push
#                      for digested refs on the local path), so the spec fails
#                      in its seed until the utils gate that; leave unset for now.
#   FUNC_BIN           func binary to use (default "func" on PATH)
#   REGISTRY           push registry prefix (default: random ttl.sh namespace)

set -o errexit
set -o nounset
set -o pipefail

# A fresh random ttl.sh namespace per run so pushes never collide; ttl.sh
# expires images within 24h by tag.
REGISTRY="${REGISTRY:-ttl.sh/funcop-e2e-$(head -c 128 </dev/urandom | LC_CTYPE=C tr -dc 'a-z0-9' | head -c 8)}"

export REGISTRY
export REGISTRY_INSECURE=false
export DEFAULT_DEPLOYER="${DEFAULT_DEPLOYER:-raw}"
export E2E_SKIP="${E2E_SKIP:-SSH|Middleware Update|registry auth secret|monorepo}"
export FUNC_REMOTE=true
export FUNC_MANAGEMENT_DISABLED=true

header=$'\e[1;33m'
reset=$'\e[0m'
function header_text { echo "$header$*$reset"; }

header_text "e2e suite with registry ${REGISTRY}, deployer ${DEFAULT_DEPLOYER}, skip '${E2E_SKIP}'"
header_text "FUNC_REMOTE=true FUNC_MANAGEMENT_DISABLED=true"

# make test-e2e reads STALE_FUNCTION_IMAGE and E2E_SKIP from the environment.
exec make test-e2e
