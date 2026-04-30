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
					ContainSubstring("handshake failed"),
					ContainSubstring("permission denied"),
					ContainSubstring("ssh:"),
				))
				return
			}
		}
		g.Expect(false).To(BeTrue(), "SourceReady condition not found")
	}
}

// createSSHFunctionAndExpectReady creates a K8s Secret with the SSH private key, creates a Function
// CR pointing at the SSH repo URL with authSecretRef, and waits for it to become Ready.
// It returns the Function name so callers can store it for cleanup.
func createSSHFunctionAndExpectReady(
	sshKeyPath, sshRepoURL, functionNamespace, namePrefix string,
) string {
	privateKeyBytes, err := os.ReadFile(sshKeyPath)
	Expect(err).NotTo(HaveOccurred())

	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "git-ssh-auth-",
			Namespace:    functionNamespace,
		},
		Data: map[string][]byte{
			"sshPrivateKey": privateKeyBytes,
		},
	}
	err = k8sClient.Create(ctx, secret)
	Expect(err).NotTo(HaveOccurred())
	utils.DeferCleanupOnSuccess(func() {
		_ = k8sClient.Delete(ctx, secret)
	})

	function := &functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: namePrefix,
			Namespace:    functionNamespace,
		},
		Spec: functionsdevv1alpha1.FunctionSpec{
			Repository: functionsdevv1alpha1.FunctionSpecRepository{
				URL: sshRepoURL,
				AuthSecretRef: &v1.LocalObjectReference{
					Name: secret.Name,
				},
			},
		},
	}

	err = k8sClient.Create(ctx, function)
	Expect(err).NotTo(HaveOccurred())

	utils.DeferCleanupOnSuccess(func() {
		_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
	})

	Eventually(functionBecomesReady(function.Name, functionNamespace)).Should(Succeed())
	return function.Name
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

	Context("with a deployed function", func() {
		var repoURL string
		var repoDir string
		var functionName, functionNamespace string

		BeforeEach(func() {
			// Create repository provider resources with automatic cleanup
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
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

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
			})

			functionName = function.Name

			Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())
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
			utils.DeferCleanupOnSuccess(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(
				repoURL,
				username,
				password,
				"go",
				utils.WithSubDir(subPath))
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)

			functionDir := filepath.Join(repoDir, subPath)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(functionDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunFunc("delete", "--path", functionDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", filepath.Join(subPath, "func.yaml"))
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
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

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
			})

			functionName = function.Name

			Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())
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
			utils.DeferCleanupOnSuccess(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
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

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
			})

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
			utils.DeferCleanupOnSuccess(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, true) // private repo
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Create access token for the user
			token, err = repoProvider.CreateAccessToken(username, password, "e2e-token")
			Expect(err).NotTo(HaveOccurred())

			// Initialize repository with function code
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			// Cleanup func deployment
			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
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
				utils.DeferCleanupOnSuccess(func() {
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

				utils.DeferCleanupOnSuccess(func() {
					_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
				})

				functionName = function.Name

				Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())
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

				utils.DeferCleanupOnSuccess(func() {
					_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
				})

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
				utils.DeferCleanupOnSuccess(func() {
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

				utils.DeferCleanupOnSuccess(func() {
					_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
				})

				functionName = function.Name

				Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())
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

				utils.DeferCleanupOnSuccess(func() {
					_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
				})

				functionName = function.Name

				Eventually(functionNotReadyWithAuthError(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
			})
		})
	})
	Context("with an SSH repository URL", func() {
		var sshRepoURL string
		var repoDir string
		var sshKeyPath string
		var functionName, functionNamespace string

		BeforeEach(func() {
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			repoName, repoURL, cleanup, err := repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Generate SSH keypair and register with Gitea
			keyDir, err := os.MkdirTemp("", "ssh-e2e-*")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, keyDir)

			sshKeyPath = filepath.Join(keyDir, "id_ed25519")
			cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", sshKeyPath, "-N", "", "-q")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			pubKeyBytes, err := os.ReadFile(sshKeyPath + ".pub")
			Expect(err).NotTo(HaveOccurred())

			err = repoProvider.CreateSSHKey(username, password, "e2e-key", string(pubKeyBytes))
			Expect(err).NotTo(HaveOccurred())

			sshRepoURL, err = repoProvider.SSHRepoURL(username, repoName)
			Expect(err).NotTo(HaveOccurred())

			// Initialize repository with function code (via HTTP)
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
		})

		It("should mark the function as ready with SSH key auth", func() {
			functionName = createSSHFunctionAndExpectReady(
				sshKeyPath, sshRepoURL, functionNamespace, "my-ssh-function-")
		})
	})
	Context("with a private SSH repository", func() {
		var sshRepoURL string
		var repoDir string
		var username, password string
		var sshKeyPath string
		var functionName, functionNamespace string

		BeforeEach(func() {
			var cleanup func()
			var err error
			var repoName string
			var repoURL string

			username, password, _, cleanup, err = repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			repoName, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, true)
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanup)

			// Generate SSH keypair and register with Gitea
			keyDir, err := os.MkdirTemp("", "ssh-e2e-*")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, keyDir)

			sshKeyPath = filepath.Join(keyDir, "id_ed25519")
			cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", sshKeyPath, "-N", "", "-q")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			pubKeyBytes, err := os.ReadFile(sshKeyPath + ".pub")
			Expect(err).NotTo(HaveOccurred())

			err = repoProvider.CreateSSHKey(username, password, "e2e-key", string(pubKeyBytes))
			Expect(err).NotTo(HaveOccurred())

			sshRepoURL, err = repoProvider.SSHRepoURL(username, repoName)
			Expect(err).NotTo(HaveOccurred())

			// Initialize repository with function code (via HTTP)
			repoDir, err = utils.InitializeRepoWithFunction(repoURL, username, password, "go")
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(os.RemoveAll, repoDir)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			utils.DeferCleanupOnSuccess(cleanupNamespaces, functionNamespace)

			// Deploy function using func CLI
			out, err := utils.RunFuncDeploy(repoDir, utils.WithNamespace(functionNamespace))
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunFunc("delete", "--path", repoDir, "--namespace", functionNamespace)
			})

			// Commit func.yaml changes
			err = utils.CommitAndPush(repoDir, "Update func.yaml after deploy", "func.yaml")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			logFailedTestDetails(functionName, functionNamespace)
		})

		It("should mark the function as ready when SSH key authSecretRef is provided", func() {
			functionName = createSSHFunctionAndExpectReady(
				sshKeyPath, sshRepoURL, functionNamespace, "my-ssh-private-function-")
		})

		It("should fail with authentication error when authSecretRef is not provided", func() {
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-ssh-private-function-noauth-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: sshRepoURL,
					},
				},
			}

			err := k8sClient.Create(ctx, function)
			Expect(err).NotTo(HaveOccurred())

			utils.DeferCleanupOnSuccess(func() {
				_, _ = utils.RunCmd("kubectl", "delete", "function", function.Name, "--namespace", function.Namespace)
			})

			functionName = function.Name

			Eventually(functionNotReadyWithAuthError(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
		})
	})
})
