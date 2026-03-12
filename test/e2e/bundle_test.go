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
	"strconv"
	"time"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
	"github.com/functions-dev/func-operator/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
)

// The bundle e2e test run with a dedicated build tag to not infer with the other tests, as the bundle offers different
// installation modes and also can make the operator to run in multiple namespaces

var _ = Describe("Bundle", Label("bundle"), Ordered, func() {

	var (
		bundleImage string // set in BeforeAll

		namespaces []string
	)

	SetDefaultEventuallyTimeout(5 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	BeforeAll(func() {
		bundleImage = os.Getenv("BUNDLE_IMG")
		Expect(bundleImage).ToNot(BeEmpty(), "BUNDLE_IMG must be given")
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			// collect logs in case it failed
			By("Collecting logs from deployed operators")
			for _, ns := range namespaces {
				By("Logs from operator in namespace " + ns)
				cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "--namespace", ns)
				controllerLogs, err := utils.Run(cmd)
				if err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
				} else {
					_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
				}
			}

			By("Collecting functions")
			cmd := exec.Command("kubectl", "get", "function", "-A", "-o", "yaml")
			out, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Functions:\n %s", out)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get functions: %s", err)
			}
		}
	})

	Context("with OwnNamespace installMode", func() {

		BeforeAll(func() {
			namespaces = createMultipleNamespaceAndDeployFunction(2)

			By("Installing the operator into " + namespaces[0])
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[0],
				"--install-mode", "OwnNamespace",
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator")
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[0])
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupNamespaces(namespaces)
			}
		})

		It("should reconcile function in own namespace", func() {
			CreateFunctionAndWaitForReady(namespaces[0])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[1])
		})
	})

	Context("with SingleNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			namespaces = createMultipleNamespaceAndDeployFunction(3)

			By("Installing the operator into " + namespaces[0] + " for " + namespaces[1])
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[0],
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", namespaces[1]),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator")
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[0])
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupNamespaces(namespaces)
			}
		})

		It("should reconcile function in dedicated namespace", func() {
			CreateFunctionAndWaitForReady(namespaces[1])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[2])
		})
	})

	Context("with MultiNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			namespaces = createMultipleNamespaceAndDeployFunction(4)

			By("Installing the operator into " + namespaces[0] + " for " + namespaces[1] + " and " + namespaces[2])
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[0],
				"--install-mode", fmt.Sprintf("MultiNamespace=%s,%s", namespaces[1], namespaces[2]),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator")
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[0])
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupNamespaces(namespaces)
			}
		})

		It("should reconcile function in dedicated namespaces", func() {
			CreateFunctionAndWaitForReady(namespaces[1])
			CreateFunctionAndWaitForReady(namespaces[2])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[3])
		})
	})

	Context("with two instances with SingleNamespace installMode installed into two distinct namespaces", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			namespaces = createMultipleNamespaceAndDeployFunction(4)

			By("Installing the operator into " + namespaces[0] + " for " + namespaces[1])
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[0],
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", namespaces[1]),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			By("Installing the operator into " + namespaces[2] + " for " + namespaces[3])
			out, err = utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[2],
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", namespaces[3]),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator from " + namespaces[0])
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[0],
					"--delete-operator-groups") // dont delete CRDs, as operator in ns3 still has them
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Uninstalling the operator from " + namespaces[2])
				out, err = utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[2])
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupNamespaces(namespaces)
			}
		})

		It("should reconcile function in dedicated namespaces", func() {
			CreateFunctionAndWaitForReady(namespaces[1])
			CreateFunctionAndWaitForReady(namespaces[3])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(namespaces[2])
		})
	})

	Context("with AllNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			namespaces = createMultipleNamespaceAndDeployFunction(2)

			By("Installing the operator into " + namespaces[0])
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", namespaces[0],
				"--install-mode", "AllNamespaces",
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator")
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", namespaces[0])
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupNamespaces(namespaces)
			}
		})

		It("should reconcile function in all namespaces", func() {
			CreateFunctionAndWaitForReady(namespaces[0])
			CreateFunctionAndWaitForReady(namespaces[1])
		})
	})
})

func CreateFunctionAndWaitForReady(namespace string) {
	// Create a Function resource
	function := &functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "my-function-",
			Namespace:    namespace,
		},
		Spec: functionsdevv1alpha1.FunctionSpec{
			Source: functionsdevv1alpha1.FunctionSpecSource{
				RepositoryURL: "https://github.com/creydr/func-go-hello-world",
			},
			Registry: functionsdevv1alpha1.FunctionSpecRegistry{
				Path:     registry,
				Insecure: registryInsecure,
			},
		},
	}

	err := k8sClient.Create(ctx, function)
	Expect(err).NotTo(HaveOccurred())

	funcBecomeReady := func(g Gomega) {
		fn := &functionsdevv1alpha1.Function{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: function.Name, Namespace: function.Namespace}, fn)
		g.Expect(err).NotTo(HaveOccurred())

		for _, cond := range fn.Status.Conditions {
			if cond.Type == functionsdevv1alpha1.TypeReady {
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				return
			}
		}
		g.Expect(false).To(BeTrue(), "Ready condition not found")
	}

	Eventually(funcBecomeReady, 5*time.Minute).Should(Succeed())
}

func CreateFunctionAndWaitForConsistentlyNotReconciled(namespace string) {
	// Create a Function resource
	function := &functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "my-function-",
			Namespace:    namespace,
		},
		Spec: functionsdevv1alpha1.FunctionSpec{
			Source: functionsdevv1alpha1.FunctionSpecSource{
				RepositoryURL: "https://github.com/creydr/func-go-hello-world",
			},
			Registry: functionsdevv1alpha1.FunctionSpecRegistry{
				Path:     registry,
				Insecure: registryInsecure,
			},
		},
	}

	err := k8sClient.Create(ctx, function)
	Expect(err).NotTo(HaveOccurred())

	funcNotReconciled := func(g Gomega) {
		fn := &functionsdevv1alpha1.Function{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: function.Name, Namespace: function.Namespace}, fn)
		g.Expect(err).NotTo(HaveOccurred())

		// If the controller reconciled this, it would have set ObservedGeneration and Conditions
		g.Expect(fn.Status.Conditions).To(BeEmpty(), "Conditions should remain empty if not reconciled")
	}

	Consistently(funcNotReconciled, time.Minute).Should(Succeed())
}

func createNamespaceAndDeployFunction() string {
	ns, err := utils.GetTestNamespace()
	Expect(err).NotTo(HaveOccurred())

	tempDir := fmt.Sprintf("%s/func-operator-e2e-%s", os.TempDir(), rand.String(10))
	Expect(err).NotTo(HaveOccurred())

	cmd := exec.Command("git", "clone", "https://github.com/creydr/func-go-hello-world", tempDir)
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	cmd = exec.Command("func", "deploy",
		"--path", tempDir,
		"--registry", registry,
		"--registry-insecure", strconv.FormatBool(registryInsecure),
		"--namespace", ns)
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())
	_, _ = fmt.Fprint(GinkgoWriter, out)

	// cleanup the repo to not run into resource issues
	cmd = exec.Command("rm", "-rf", tempDir)
	_, err = utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred())

	return ns
}

// createMultipleNamespaceAndDeployFunction creates multiple namespaces with functions
func createMultipleNamespaceAndDeployFunction(count int) []string {
	namespaces := make([]string, count)

	for i := 0; i < count; i++ {
		// parallelizing this via goroutines seems to lead to resource issues, therefore keeping it sequential
		namespaces[i] = createNamespaceAndDeployFunction()
	}

	return namespaces
}

func cleanupNamespaces(namespaces []string) {
	By("Cleaning up all resources")
	for _, ns := range namespaces {
		cmd := exec.Command("kubectl", "delete", "namespace", ns, "--ignore-not-found")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	}
}
