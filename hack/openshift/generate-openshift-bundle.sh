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
Equivalent to "make bundle-combined CHANNELS=tech-preview-v2" but adds
faas-console-plugin support (image env var, RBAC, configmap flag).

By default, images are read from the existing ${CSV_FILE} (if present).

Options:
  --func-operator-image IMAGE              func-operator image
  --objectbucketsource-adapter-image IMAGE objectbucket-notifications-adapter image
  --console-plugin-image IMAGE             faas-console-plugin image
  --version VERSION                        Bundle version (default: 0.0.1)
  --channels CHANNELS                      Bundle channels (default: tech-preview-v2)
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
VERSION="0.0.1"
CHANNELS="tech-preview-v2"

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

# Validate the bundle
${OPERATOR_SDK} bundle validate ./bundle

echo ""
echo "OpenShift bundle generated successfully in bundle/"
