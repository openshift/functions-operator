/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/functions-dev/func-operator/test/utils"
	. "github.com/onsi/ginkgo/v2"
)

// namespace where the project is deployed in
const namespace = "func-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "func-operator-controller-manager"

func deployerIsKedaOrRaw() bool {
	d := os.Getenv("DEFAULT_DEPLOYER")
	return d == "keda" || d == "raw"
}

// Newest knative/func that still ships go/http middleware func-go v0.21.3
// and has knative, raw, and keda. v1.22.3 writes func.yaml before a remote
// pipeline upload so --deployer is on disk for func-util. Current CLI
// (1.23+) ships func-go v0.22.0, so this seed makes the operator redeploy.
const staleFuncCLI = "v1.22.3"
const staleMiddlewareVersion = "v0.21.3"

// workloadKind is the cluster object that holds the function pod template.
// keda/raw use a Deployment; knative uses a Service (ksvc).
func workloadKind() string {
	if deployerIsKedaOrRaw() {
		return "deploy"
	}
	return "ksvc"
}

// logFailedTestDetails logs function resource and controller logs on test failure
func logFailedTestDetails(functionName, functionNamespace string) {
	specReport := CurrentSpecReport()
	if !specReport.Failed() {
		return
	}

	if functionName != "" {
		cmd := exec.Command("kubectl", "get", "function", functionName, "-n", functionNamespace, "-o", "yaml")
		function, err := utils.Run(cmd)
		if err == nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Function:\n %s", function)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get function: %s", err)
		}
	}

	By("Fetching controller manager pod logs")
	cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace, "--tail", "20")
	controllerLogs, err := utils.Run(cmd)
	if err == nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
	}
}
