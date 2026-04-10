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
	"path/filepath"
	"time"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
	"github.com/functions-dev/func-operator/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// expectFunctionConditionTrue returns a Gomega function that checks if a Function
// has the specified condition type with status True
func expectFunctionConditionTrue(functionName, functionNamespace string, conditionType string) func(g Gomega) {
	return func(g Gomega) {
		fn := &functionsdevv1alpha1.Function{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: functionName, Namespace: functionNamespace}, fn)
		g.Expect(err).NotTo(HaveOccurred())

		for _, cond := range fn.Status.Conditions {
			if cond.Type == conditionType {
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				return
			}
		}
		g.Expect(false).To(BeTrue(), conditionType+" condition not found")
	}
}

// expectFunctionConditionFalseWithReason returns a Gomega function that checks if a Function
// has the specified condition type with status False, specific reason, and message substring
func expectFunctionConditionFalseWithReason(
	functionName,
	functionNamespace,
	conditionType,
	reason,
	messageSubstring string) func(g Gomega) {
	return func(g Gomega) {
		fn := &functionsdevv1alpha1.Function{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: functionName, Namespace: functionNamespace}, fn)
		g.Expect(err).NotTo(HaveOccurred())

		for _, cond := range fn.Status.Conditions {
			if cond.Type == conditionType {
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(reason))
				g.Expect(cond.Message).To(ContainSubstring(messageSubstring))
				return
			}
		}
		g.Expect(false).To(BeTrue(), conditionType+" condition not found")
	}
}

// functionBecomesReady is a convenience wrapper for checking if a Function becomes Ready
func functionBecomesReady(functionName, functionNamespace string) func(g Gomega) {
	return expectFunctionConditionTrue(functionName, functionNamespace, functionsdevv1alpha1.TypeReady)
}

// functionMiddlewareUpToDate checks if the middleware condition is true
func functionMiddlewareUpToDate(functionName, functionNamespace string) func(g Gomega) {
	return expectFunctionConditionTrue(functionName, functionNamespace, functionsdevv1alpha1.TypeMiddlewareUpToDate)
}

// functionNotReadyWithAuthError returns a Gomega function that checks if a Function
// is NOT Ready and has an auth error in SourceReady condition
func functionNotReadyWithAuthError(functionName, functionNamespace string) func(g Gomega) {
	return func(g Gomega) {
		fn := &functionsdevv1alpha1.Function{}
		err := k8sClient.Get(ctx, types.NamespacedName{Name: functionName, Namespace: functionNamespace}, fn)
		g.Expect(err).NotTo(HaveOccurred())

		for _, cond := range fn.Status.Conditions {
			if cond.Type == functionsdevv1alpha1.TypeReady {
				g.Expect(cond.Status).NotTo(Equal(metav1.ConditionTrue))
			}

			// Check for SourceReady condition with auth error
			if cond.Type == functionsdevv1alpha1.TypeSourceReady {
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(Or(
					ContainSubstring("authentication"),
					ContainSubstring("Authentication"),
					ContainSubstring("401"),
					ContainSubstring("Unauthorized"),
				))
				return
			}
		}
		g.Expect(false).To(BeTrue(), "SourceReady condition not found")
	}
}

// functionNotDeployed check if the function is not ready as the function was not deployed yet
func functionNotDeployed(functionName, functionNamespace string) func(g Gomega) {
	return expectFunctionConditionFalseWithReason(
		functionName,
		functionNamespace,
		functionsdevv1alpha1.TypeDeployed,
		"NotDeployed",
		"Function not deployed yet")
}

var _ = Describe("Operator", func() {

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("with a deployed function", func() {
		var repoURL string
		var repoDir string
		var functionName, functionNamespace string

		BeforeEach(func() {
			// Create repository provider resources with automatic cleanup
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			DeferCleanup(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)

			// Cleanup function resource
			if functionName != "" {
				cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should mark the function as ready", func() {
			// Create a Function resource
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-function-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: repoURL,
					},
				},
			}

			err := k8sClient.Create(ctx, function)
			Expect(err).NotTo(HaveOccurred())

			functionName = function.Name

			// redeploy could take a bit longer therefore give a bit more time
			Eventually(functionBecomesReady(functionName, functionNamespace), 6*time.Minute).Should(Succeed())
		})
	})
	Context("with a function in a subdirectory in a monorepo", func() {
		var repoURL string
		var repoDir string
		const subPath = "function-subdir"
		var functionName, functionNamespace string

		BeforeEach(func() {
			// Create repository provider resources with automatic cleanup
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(
				repoURL,
				username,
				password,
				"go",
				utils.WithSubDir(subPath))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)

			functionDir := filepath.Join(repoDir, subPath)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(functionDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			DeferCleanup(func() {
				_, _ = utils.RunFunc("delete", "--path", functionDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", filepath.Join(subPath, "func.yaml"))
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)

			// Cleanup function resource
			if functionName != "" {
				cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should mark the function as ready", func() {
			// Create a Function resource
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-function-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL:  repoURL,
						Path: subPath,
					},
				},
			}

			err := k8sClient.Create(ctx, function)
			Expect(err).NotTo(HaveOccurred())

			functionName = function.Name

			// redeploy could take a bit longer therefore give a bit more time
			Eventually(functionBecomesReady(functionName, functionNamespace), 6*time.Minute).Should(Succeed())
		})
	})
	Context("with a not yet deployed function", func() {
		var repoURL string
		var repoDir string
		var functionName, functionNamespace string

		BeforeEach(func() {
			var err error

			// Create repository with function code but don't deploy
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)
		})

		AfterEach(func() {
			// Cleanup function resource
			if functionName != "" {
				cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		It("should mark the function as not ready", func() {
			// Create a Function resource
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-undeployed-function-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: repoURL,
					},
				},
			}

			err := k8sClient.Create(ctx, function)
			Expect(err).NotTo(HaveOccurred())

			functionName = function.Name

			Eventually(functionNotDeployed(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
		})
	})
	Context("with a private repository", func() {
		var repoURL string
		var repoDir string
		var username, password, token string
		var functionName, functionNamespace string

		BeforeEach(func() {
			// Create repository provider resources with automatic cleanup
			var cleanup func()
			var err error

			username, password, _, cleanup, err = repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, true) // private repo
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			// Create access token for the user
			token, err = repoProvider.CreateAccessToken(username, password, "e2e-token")
			Expect(err).NotTo(HaveOccurred())

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			DeferCleanup(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)

			// Cleanup function resource
			if functionName != "" {
				cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}
		})

		Context("using token authentication", func() {
			It("should mark the function as ready when authSecretRef is provided", func() {
				// Create auth secret with token
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "git-auth-",
						Namespace:    functionNamespace,
					},
					Data: map[string][]byte{
						"token": []byte(token),
					},
				}
				err := k8sClient.Create(ctx, secret)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, secret)
				})

				// Create a Function resource with authSecretRef
				function := &functionsdevv1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "my-private-function-",
						Namespace:    functionNamespace,
					},
					Spec: functionsdevv1alpha1.FunctionSpec{
						Repository: functionsdevv1alpha1.FunctionSpecRepository{
							URL: repoURL,
							AuthSecretRef: &v1.LocalObjectReference{
								Name: secret.Name,
							},
						},
					},
				}

				err = k8sClient.Create(ctx, function)
				Expect(err).NotTo(HaveOccurred())

				functionName = function.Name

				Eventually(functionBecomesReady(functionName, functionNamespace), 6*time.Minute).Should(Succeed())
			})

			It("should fail with authentication error when authSecretRef is not provided", func() {
				// Create a Function resource WITHOUT authSecretRef for private repo
				function := &functionsdevv1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "my-private-function-noauth-",
						Namespace:    functionNamespace,
					},
					Spec: functionsdevv1alpha1.FunctionSpec{
						Repository: functionsdevv1alpha1.FunctionSpecRepository{
							URL: repoURL,
							// No AuthSecretRef
						},
					},
				}

				err := k8sClient.Create(ctx, function)
				Expect(err).NotTo(HaveOccurred())

				functionName = function.Name

				Eventually(functionNotReadyWithAuthError(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
			})
		})

		Context("using username/password authentication", func() {
			It("should mark the function as ready when authSecretRef is provided", func() {
				// Create auth secret with username and password
				secret := &v1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "git-auth-",
						Namespace:    functionNamespace,
					},
					Data: map[string][]byte{
						"username": []byte(username),
						"password": []byte(password),
					},
				}
				err := k8sClient.Create(ctx, secret)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, secret)
				})

				// Create a Function resource with authSecretRef
				function := &functionsdevv1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "my-private-function-",
						Namespace:    functionNamespace,
					},
					Spec: functionsdevv1alpha1.FunctionSpec{
						Repository: functionsdevv1alpha1.FunctionSpecRepository{
							URL: repoURL,
							AuthSecretRef: &v1.LocalObjectReference{
								Name: secret.Name,
							},
						},
					},
				}

				err = k8sClient.Create(ctx, function)
				Expect(err).NotTo(HaveOccurred())

				functionName = function.Name

				Eventually(functionBecomesReady(functionName, functionNamespace), 6*time.Minute).Should(Succeed())
			})

			It("should fail with authentication error when authSecretRef is not provided", func() {
				// Create a Function resource WITHOUT authSecretRef for private repo
				function := &functionsdevv1alpha1.Function{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "my-private-function-noauth-",
						Namespace:    functionNamespace,
					},
					Spec: functionsdevv1alpha1.FunctionSpec{
						Repository: functionsdevv1alpha1.FunctionSpecRepository{
							URL: repoURL,
							// No AuthSecretRef
						},
					},
				}

				err := k8sClient.Create(ctx, function)
				Expect(err).NotTo(HaveOccurred())

				functionName = function.Name

				Eventually(functionNotReadyWithAuthError(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
			})
		})
	})
})
