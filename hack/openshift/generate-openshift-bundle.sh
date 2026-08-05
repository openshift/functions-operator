#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

cd "${REPO_ROOT}"

CSV_FILE="bundle/manifests/serverless-functions.clusterserviceversion.yaml"

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Generate the OpenShift bundle for the functions operator.
Equivalent to "make bundle-combined CHANNELS=candidate-v2" but adds
faas-console-plugin support (image env var, RBAC, configmap flag).

By default, images are read from the existing ${CSV_FILE} (if present).

Options:
  --func-operator-image IMAGE              func-operator image
  --objectbucketsource-adapter-image IMAGE objectbucket-notifications-adapter image
  --console-plugin-image IMAGE             faas-console-plugin image
  --version VERSION                        Bundle version
  --channels CHANNELS                      Bundle channels (default: candidate-v2)
  -h, --help                               Show this help message

Examples:
  # Use images from existing CSV:
  ./hack/openshift/generate-openshift-bundle.sh

  # Override all images:
  ./hack/openshift/generate-openshift-bundle.sh \\
    --func-operator-image quay.io/my/func-operator@sha256:abc123 \\
    --objectbucketsource-adapter-image quay.io/my/objectbucket@sha256:def456 \\
    --console-plugin-image quay.io/my/console-plugin@sha256:789abc
EOF
}

extract_image_from_csv() {
    local deployment_name="$1"
    local container_name="$2"

    if [[ ! -f "${CSV_FILE}" ]]; then
        return 1
    fi

    # Use yq to extract the image from the deployment spec in the CSV
    local image
    image=$(yq eval "
        .spec.install.spec.deployments[]
        | select(.name == \"${deployment_name}\")
        | .spec.template.spec.containers[]
        | select(.name == \"${container_name}\")
        | .image
    " "${CSV_FILE}" 2>/dev/null) || return 1

    if [[ -n "${image}" && "${image}" != "null" ]]; then
        echo "${image}"
        return 0
    fi
    return 1
}

extract_console_plugin_image_from_csv() {
    if [[ ! -f "${CSV_FILE}" ]]; then
        return 1
    fi

    local image
    image=$(yq eval "
        .spec.install.spec.deployments[]
        | select(.name == \"func-operator-controller-manager\")
        | .spec.template.spec.containers[]
        | select(.name == \"manager\")
        | .env[]
        | select(.name == \"CONSOLE_PLUGIN_IMAGE\")
        | .value
    " "${CSV_FILE}" 2>/dev/null) || return 1

    if [[ -n "${image}" && "${image}" != "null" ]]; then
        echo "${image}"
        return 0
    fi
    return 1
}

FUNC_OPERATOR_IMAGE=""
OBJECTBUCKETSOURCE_ADAPTER_IMAGE=""
CONSOLE_PLUGIN_IMAGE=""
VERSION="2.0.1"
CHANNELS="candidate-v2"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --func-operator-image)
            FUNC_OPERATOR_IMAGE="$2"; shift 2 ;;
        --objectbucketsource-adapter-image)
            OBJECTBUCKETSOURCE_ADAPTER_IMAGE="$2"; shift 2 ;;
        --console-plugin-image)
            CONSOLE_PLUGIN_IMAGE="$2"; shift 2 ;;
        --version)
            VERSION="$2"; shift 2 ;;
        --channels)
            CHANNELS="$2"; shift 2 ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            echo "Unknown option: $1" >&2; usage >&2; exit 1 ;;
    esac
done

# Resolve tools
KUSTOMIZE="${REPO_ROOT}/bin/kustomize"
if [[ ! -x "${KUSTOMIZE}" ]]; then
    echo "kustomize not found at ${KUSTOMIZE}, run 'make kustomize' first" >&2
    exit 1
fi

OPERATOR_SDK="$(command -v operator-sdk 2>/dev/null || echo "${REPO_ROOT}/bin/operator-sdk")"
if [[ ! -x "${OPERATOR_SDK}" ]]; then
    echo "operator-sdk not found, run 'make operator-sdk' first" >&2
    exit 1
fi

YQ="${REPO_ROOT}/bin/yq"
if [[ ! -x "${YQ}" ]]; then
    YQ="$(command -v yq 2>/dev/null || true)"
fi
if [[ -z "${YQ}" || ! -x "${YQ}" ]]; then
    echo "yq not found, run 'make yq' first" >&2
    exit 1
fi
# Export yq for use in the script (yq function above uses the variable)
yq() { "${YQ}" "$@"; }

# Read defaults from existing CSV if images not specified
if [[ -z "${FUNC_OPERATOR_IMAGE}" ]]; then
    FUNC_OPERATOR_IMAGE=$(extract_image_from_csv "func-operator-controller-manager" "manager") || true
    if [[ -z "${FUNC_OPERATOR_IMAGE}" ]]; then
        echo "Error: --func-operator-image is required (no existing CSV to read from)" >&2
        exit 1
    fi
    echo "Using func-operator image from existing CSV: ${FUNC_OPERATOR_IMAGE}"
fi

if [[ -z "${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}" ]]; then
    OBJECTBUCKETSOURCE_ADAPTER_IMAGE=$(extract_image_from_csv "objectbucket-notifications-adapter-manager" "manager") || true
    if [[ -z "${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}" ]]; then
        echo "Error: --objectbucketsource-adapter-image is required (no existing CSV to read from)" >&2
        exit 1
    fi
    echo "Using objectbucketsource-adapter image from existing CSV: ${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}"
fi

if [[ -z "${CONSOLE_PLUGIN_IMAGE}" ]]; then
    CONSOLE_PLUGIN_IMAGE=$(extract_console_plugin_image_from_csv) || true
    if [[ -z "${CONSOLE_PLUGIN_IMAGE}" ]]; then
        echo "Warning: faas-console-plugin image not found in existing CSV, --console-plugin-image is required" >&2
        exit 1
    fi
    echo "Using faas-console-plugin image from existing CSV: ${CONSOLE_PLUGIN_IMAGE}"
fi

echo ""
echo "Generating OpenShift bundle with:"
echo "  func-operator:              ${FUNC_OPERATOR_IMAGE}"
echo "  objectbucketsource-adapter: ${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}"
echo "  faas-console-plugin:        ${CONSOLE_PLUGIN_IMAGE}"
echo "  version:                    ${VERSION}"
echo "  channels:                   ${CHANNELS}"
echo ""

# Set images in kustomize overlays
cd config/manager && ${KUSTOMIZE} edit set image controller="${FUNC_OPERATOR_IMAGE}"
cd "${REPO_ROOT}"

cd config/sources/objectbucket/manager && ${KUSTOMIZE} edit set image "source-objectbucket-adapter=${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}"
cd "${REPO_ROOT}"
cd config/combined/source-objectbucket && ${KUSTOMIZE} edit set image "source-objectbucket-adapter=${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}"
cd "${REPO_ROOT}"

# Set the console plugin image in the manager patch files (both copies)
MANAGER_PATCH="config/openshift/manager_patch.yaml"
MANIFESTS_MANAGER_PATCH="config/openshift/manifests/manager_patch.yaml"

yq eval -i "
  (.spec.template.spec.containers[] | select(.name == \"manager\") | .env[] | select(.name == \"CONSOLE_PLUGIN_IMAGE\") | .value) = \"${CONSOLE_PLUGIN_IMAGE}\"
" "${MANAGER_PATCH}"

yq eval -i "
  (.spec.template.spec.containers[] | select(.name == \"manager\") | .env[] | select(.name == \"CONSOLE_PLUGIN_IMAGE\") | .value) = \"${CONSOLE_PLUGIN_IMAGE}\"
" "${MANIFESTS_MANAGER_PATCH}"

# Generate the bundle
${KUSTOMIZE} build config/openshift/manifests | ${OPERATOR_SDK} generate bundle \
    -q --overwrite \
    --version "${VERSION}" \
    --channels "${CHANNELS}"

# Add console plugin annotation so OLM/OpenShift console knows this operator provides a plugin.
yq eval -i '
  .metadata.annotations."console.openshift.io/plugins" = "[\"console-functions-plugin\"]"
' "${CSV_FILE}"

# Add spec.relatedImages so OLM can mirror all operand images for
# disconnected installs.
yq eval -i "
  .spec.relatedImages = [
    {\"name\": \"func-operator\", \"image\": \"${FUNC_OPERATOR_IMAGE}\"},
    {\"name\": \"objectbucket-notifications-adapter\", \"image\": \"${OBJECTBUCKETSOURCE_ADAPTER_IMAGE}\"},
    {\"name\": \"faas-console-plugin\", \"image\": \"${CONSOLE_PLUGIN_IMAGE}\"}
  ]
" "${CSV_FILE}"

# Patch CSV metadata fields that operator-sdk generate bundle overwrites.
ICON_BASE64="PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIzOCIgaGVpZ2h0PSIzOCIgdmlld0JveD0iMCAwIDM4IDM4Ij48ZGVmcz48c3R5bGU+LmF7ZmlsbDojZmZmO30uYntmaWxsOiNlMDA7fTwvc3R5bGU+PC9kZWZzPjxwYXRoIGNsYXNzPSJhIiBkPSJNMjgsMUgxMGE5LDksMCwwLDAtOSw5VjI4YTksOSwwLDAsMCw5LDlIMjhhOSw5LDAsMCwwLDktOVYxMGE5LDksMCwwLDAtOS05WiIvPjxwYXRoIGQ9Ik0yOCwyLjI1QTcuNzU4Nyw3Ljc1ODcsMCwwLDEsMzUuNzUsMTBWMjhBNy43NTg3LDcuNzU4NywwLDAsMSwyOCwzNS43NUgxMEE3Ljc1ODcsNy43NTg3LDAsMCwxLDIuMjUsMjhWMTBBNy43NTg3LDcuNzU4NywwLDAsMSwxMCwyLjI1SDI4TTI4LDFIMTBhOSw5LDAsMCwwLTksOVYyOGE5LDksMCwwLDAsOSw5SDI4YTksOSwwLDAsMCw5LTlWMTBhOSw5LDAsMCwwLTktOVoiLz48cGF0aCBjbGFzcz0iYiIgZD0iTTE0LDIzLjQ3NjZIMTBhLjYyNTMuNjI1MywwLDAsMS0uNjI1LS42MjV2LTRhLjYyNTIuNjI1MiwwLDAsMSwuNjI1LS42MjVoNGEuNjI1Mi42MjUyLDAsMCwxLC42MjUuNjI1djRBLjYyNTMuNjI1MywwLDAsMSwxNCwyMy40NzY2Wm0tMy4zNzUtMS4yNWgyLjc1di0yLjc1aC0yLjc1WiIvPjxwYXRoIGNsYXNzPSJiIiBkPSJNMjEsMjMuNDc2NkgxN2EuNjI1My42MjUzLDAsMCwxLS42MjUtLjYyNXYtNGEuNjI1Mi42MjUyLDAsMCwxLC42MjUtLjYyNWg0YS42MjUyLjYyNTIsMCwwLDEsLjYyNS42MjV2NEEuNjI1My42MjUzLDAsMCwxLDIxLDIzLjQ3NjZabS0zLjM3NS0xLjI1aDIuNzV2LTIuNzVoLTIuNzVaIi8+PHBhdGggY2xhc3M9ImIiIGQ9Ik0xNy41LDE2LjQ3NjZoLTRhLjYyNTMuNjI1MywwLDAsMS0uNjI1LS42MjV2LTRhLjYyNTIuNjI1MiwwLDAsMSwuNjI1LS42MjVoNGEuNjI1Mi42MjUyLDAsMCwxLC42MjUuNjI1djRBLjYyNTMuNjI1MywwLDAsMSwxNy41LDE2LjQ3NjZabS0zLjM3NS0xLjI1aDIuNzV2LTIuNzVoLTIuNzVaIi8+PHBhdGggY2xhc3M9ImIiIGQ9Ik0yNC41LDE2LjQ3NjZoLTRhLjYyNTMuNjI1MywwLDAsMS0uNjI1LS42MjV2LTRhLjYyNTIuNjI1MiwwLDAsMSwuNjI1LS42MjVoNGEuNjI1Mi42MjUyLDAsMCwxLC42MjUuNjI1djRBLjYyNTMuNjI1MywwLDAsMSwyNC41LDE2LjQ3NjZabS0zLjM3NS0xLjI1aDIuNzV2LTIuNzVoLTIuNzVaIi8+PHBhdGggY2xhc3M9ImIiIGQ9Ik0yOCwyMy40NzY2SDI0YS42MjUzLjYyNTMsMCwwLDEtLjYyNS0uNjI1di00YS42MjUyLjYyNTIsMCwwLDEsLjYyNS0uNjI1aDRhLjYyNTIuNjI1MiwwLDAsMSwuNjI1LjYyNXY0QS42MjUzLjYyNTMsMCwwLDEsMjgsMjMuNDc2NlptLTMuMzc1LTEuMjVoMi43NXYtMi43NWgtMi43NVoiLz48cGF0aCBkPSJNMjksMjYuNDc2Nkg5YS42MjUuNjI1LDAsMCwxLDAtMS4yNUgyOWEuNjI1LjYyNSwwLDAsMSwwLDEuMjVaIi8+PC9zdmc+"

export DESCRIPTION
read -r -d '' DESCRIPTION <<'DESCEOF' || true
Serverless Functions Operator provides
- an operator for managing Serverless Functions runtimes, using the Functions CRD
- an OpenShift Console plugin to deploy and manage Functions
- event sources for triggering functions.

# Prerequisites
- OpenShift Pipelines
- OpenShift Serverless Operator

# Further Information
For documentation on OpenShift Serverless, see:
- [Installation Guide](https://docs.redhat.com/en/documentation/red_hat_openshift_serverless/1.37/html/installing_openshift_serverless/index)
- [Develop Serverless Applications](https://docs.redhat.com/en/documentation/red_hat_openshift_serverless/1.37/html/serving/getting-started-with-knative-serving#serverless-applications)
DESCEOF

yq eval -i "
  .spec.displayName = \"Serverless Functions Operator\" |
  .spec.icon[0].base64data = \"${ICON_BASE64}\" |
  .spec.icon[0].mediatype = \"image/svg+xml\" |
  .spec.keywords = [\"eventing\", \"faas\", \"functions\", \"scale\", \"serverless\", \"serving\", \"zero\"] |
  .spec.links = [{\"name\": \"Documentation\", \"url\": \"https://docs.redhat.com/en/documentation/red_hat_openshift_serverless/\"}, {\"name\": \"Source Repository\", \"url\": \"https://github.com/openshift/functions-operator\"}] |
  .spec.maintainers = [{\"email\": \"support@redhat.com\", \"name\": \"Serverless Team\"}] |
  .spec.provider.name = \"Red Hat\" |
  del(.spec.provider.url)
" "${CSV_FILE}"

yq eval -i '
  .spec.description = strenv(DESCRIPTION) |
  .spec.description style="literal"
' "${CSV_FILE}"

# Validate the bundle
${OPERATOR_SDK} bundle validate ./bundle

echo ""
echo "OpenShift bundle generated successfully in bundle/"
