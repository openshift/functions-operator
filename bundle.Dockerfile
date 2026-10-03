ARG GO_RUNTIME=registry.access.redhat.com/ubi9/ubi-minimal@sha256:1d7c5517a4a1a8e2688620b39ee980e82505ca1ab7ae5541b5463120ae9b3897
FROM $GO_RUNTIME

# Core bundle labels.
LABEL operators.operatorframework.io.bundle.mediatype.v1=registry+v1
LABEL operators.operatorframework.io.bundle.manifests.v1=manifests/
LABEL operators.operatorframework.io.bundle.metadata.v1=metadata/
LABEL operators.operatorframework.io.bundle.package.v1=serverless-functions
LABEL operators.operatorframework.io.bundle.channels.v1=candidate-v2
LABEL operators.operatorframework.io.metrics.builder=operator-sdk-v1.42.2+git
LABEL operators.operatorframework.io.metrics.mediatype.v1=metrics+v1
LABEL operators.operatorframework.io.metrics.project_layout=go.kubebuilder.io/v4

# Labels for testing.
LABEL operators.operatorframework.io.test.mediatype.v1=scorecard+v1
LABEL operators.operatorframework.io.test.config.v1=tests/scorecard/

# Copy files to locations specified by labels.
COPY bundle/manifests /manifests/
COPY bundle/metadata /metadata/
COPY bundle/tests/scorecard /tests/scorecard/

LABEL name="openshift-serverless-tech-preview/functions-operator-bundle" \
      com.redhat.component="openshift-serverless-functions-operator-bundle-container" \
      version="2.0" \
      release="1" \
      summary="OpenShift Serverless Functions Operator bundle" \
      description="Contains an OLM bundle of the OpenShift Serverless Functions Operator" \
      io.k8s.display-name="OpenShift Serverless Functions Operator bundle" \
      io.k8s.description="Contains an OLM bundle of the OpenShift Serverless Functions Operator" \
      io.openshift.tags="openshift,serverless,functions,operator,bundle" \
      maintainer="serverless-support@redhat.com" \
      cpe="cpe:/a:redhat:openshift_serverless:2.0::el9"
