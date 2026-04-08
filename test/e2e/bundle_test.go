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
	"time"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
	"github.com/functions-dev/func-operator/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// The bundle e2e test run with a dedicated build tag to not infer with the other tests, as the bundle offers different
// installation modes and also can make the operator to run in multiple namespaces

// TestNamespace represents a test namespace with its associated repository
type TestNamespace struct {
	Name    string
	RepoURL string
}

var _ = Describe("Bundle", Label("bundle"), Ordered, func() {

	var (
		bundleImage string // set in BeforeAll

		testNamespaces []TestNamespace
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
			for _, testNs := range testNamespaces {
				By("Logs from operator in namespace " + testNs.Name)
				cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "--namespace", testNs.Name)
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
			testNamespaces = createMultipleNamespaceAndDeployFunction(2)

			By("Installing the operator into " + testNamespaces[0].Name)
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[0].Name,
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
					"--namespace", testNamespaces[0].Name)
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupTestNamespaces(testNamespaces...)
			}
		})

		It("should reconcile function in own namespace", func() {
			CreateFunctionAndWaitForReady(testNamespaces[0])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[1])
		})
	})

	Context("with SingleNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			testNamespaces = createMultipleNamespaceAndDeployFunction(3)

			By("Installing the operator into " + testNamespaces[0].Name + " for " + testNamespaces[1].Name)
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[0].Name,
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", testNamespaces[1].Name),
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
					"--namespace", testNamespaces[0].Name)
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupTestNamespaces(testNamespaces...)
			}
		})

		It("should reconcile function in dedicated namespace", func() {
			CreateFunctionAndWaitForReady(testNamespaces[1])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[2])
		})
	})

	Context("with MultiNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			testNamespaces = createMultipleNamespaceAndDeployFunction(4)

			By("Installing the operator into " + testNamespaces[0].Name +
				" for " + testNamespaces[1].Name + " and " + testNamespaces[2].Name)
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[0].Name,
				"--install-mode", fmt.Sprintf("MultiNamespace=%s,%s", testNamespaces[1].Name, testNamespaces[2].Name),
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
					"--namespace", testNamespaces[0].Name)
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupTestNamespaces(testNamespaces...)
			}
		})

		It("should reconcile function in dedicated namespaces", func() {
			CreateFunctionAndWaitForReady(testNamespaces[1])
			CreateFunctionAndWaitForReady(testNamespaces[2])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[3])
		})
	})

	Context("with two instances with SingleNamespace installMode installed into two distinct namespaces", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			testNamespaces = createMultipleNamespaceAndDeployFunction(4)

			By("Installing the operator into " + testNamespaces[0].Name + " for " + testNamespaces[1].Name)
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[0].Name,
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", testNamespaces[1].Name),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			By("Installing the operator into " + testNamespaces[2].Name + " for " + testNamespaces[3].Name)
			out, err = utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[2].Name,
				"--install-mode", fmt.Sprintf("SingleNamespace=%s", testNamespaces[3].Name),
				fmt.Sprintf("--skip-tls-verify=%v", registryInsecure),
				bundleImage)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterAll(func() {
			specReport := CurrentSpecReport()
			if !specReport.Failed() {
				By("Uninstalling the operator from " + testNamespaces[0].Name)
				out, err := utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", testNamespaces[0].Name,
					"--delete-operator-groups") // dont delete CRDs, as operator in ns3 still has them
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Uninstalling the operator from " + testNamespaces[2].Name)
				out, err = utils.OperatorSdkRun("cleanup",
					"func-operator",
					"--namespace", testNamespaces[2].Name)
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupTestNamespaces(testNamespaces...)
			}
		})

		It("should reconcile function in dedicated namespaces", func() {
			CreateFunctionAndWaitForReady(testNamespaces[1])
			CreateFunctionAndWaitForReady(testNamespaces[3])
		})
		It("should not reconcile function in other namespace", func() {
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[0])
			CreateFunctionAndWaitForConsistentlyNotReconciled(testNamespaces[2])
		})
	})

	Context("with AllNamespace installMode", func() {
		BeforeAll(func() {
			By("Setting up test namespaces")
			testNamespaces = createMultipleNamespaceAndDeployFunction(2)

			By("Installing the operator into " + testNamespaces[0].Name)
			out, err := utils.OperatorSdkRun("run", "bundle",
				"--namespace", testNamespaces[0].Name,
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
					"--namespace", testNamespaces[0].Name)
				Expect(err).NotTo(HaveOccurred())
				_, _ = fmt.Fprint(GinkgoWriter, out)

				By("Cleanup resources")
				cleanupTestNamespaces(testNamespaces...)
			}
		})

		It("should reconcile function in all namespaces", func() {
			CreateFunctionAndWaitForReady(testNamespaces[0])
			CreateFunctionAndWaitForReady(testNamespaces[1])
		})
	})
})

func CreateFunctionAndWaitForReady(testNs TestNamespace) {
	// Create a Function resource
	function := &functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "my-function-",
			Namespace:    testNs.Name,
		},
		Spec: functionsdevv1alpha1.FunctionSpec{
			Repository: functionsdevv1alpha1.FunctionSpecRepository{
				URL: testNs.RepoURL,
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

func CreateFunctionAndWaitForConsistentlyNotReconciled(testNs TestNamespace) {
	// Create a Function resource
	function := &functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "my-function-",
			Namespace:    testNs.Name,
		},
		Spec: functionsdevv1alpha1.FunctionSpec{
			Repository: functionsdevv1alpha1.FunctionSpecRepository{
				URL: testNs.RepoURL,
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

func createNamespaceAndDeployFunction() TestNamespace {
	var err error
	ns, err := utils.GetTestNamespace()
	Expect(err).NotTo(HaveOccurred())

	// Create repository provider resources
	username, password, _, cleanup, err := repoProvider.CreateRandomUser()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanup)

	_, repoURL, cleanup, err := repoProvider.CreateRandomRepo(username, false)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(cleanup)

	// Initialize repo with function code
	repoDir, err := utils.InitializeRepoWithFunction(repoURL, username, password, "go")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(os.RemoveAll, repoDir)

	// Deploy function
	out, err := utils.RunFunc("deploy",
		"--namespace", ns,
		"--path", repoDir,
		"--registry", registry,
		fmt.Sprintf("--registry-insecure=%t", registryInsecure))
	Expect(err).NotTo(HaveOccurred())
	_, _ = fmt.Fprint(GinkgoWriter, out)

	// Push updated func.yaml back to repo
	err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
	Expect(err).NotTo(HaveOccurred())

	return TestNamespace{Name: ns, RepoURL: repoURL}
}

// createMultipleNamespaceAndDeployFunction creates multiple namespaces with functions
func createMultipleNamespaceAndDeployFunction(count int) []TestNamespace {
	testNamespaces := make([]TestNamespace, count)

	for i := 0; i < count; i++ {
		// parallelizing this via goroutines seems to lead to resource issues, therefore keeping it sequential
		testNamespaces[i] = createNamespaceAndDeployFunction()
	}

	return testNamespaces
}

func cleanupTestNamespaces(testNamespaces ...TestNamespace) {
	By("Cleaning up test namespaces resources")
	for _, testNs := range testNamespaces {
		cleanupNamespaces(testNs.Name)
	}
}

func cleanupNamespaces(namespaces ...string) {
	for _, ns := range namespaces {
		By("Cleaning up namespace " + ns)
		cmd := exec.Command("kubectl", "delete", "namespace", ns, "--ignore-not-found")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
	}
}
