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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
	"github.com/functions-dev/func-operator/internal/function"
	"github.com/functions-dev/func-operator/test/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	funcfn "knative.dev/func/pkg/functions"
)

var _ = Describe("Middleware Update", func() {

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("with a function deployed using old func CLI", func() {

		var repoURL string
		var repoDir string
		var functionName, functionNamespace string

		BeforeEach(func() {
			if os.Getenv("DEFAULT_DEPLOYER") == "keda" || os.Getenv("DEFAULT_DEPLOYER") == "raw" {
				Skip("Skipping middleware test for Keda & raw deployer, " +
					"as those are not supported on used CLI version (1.20.x) of this tests")
			}

			var err error

			// Create repository provider resources with automatic cleanup
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)

			// Initialize repository with function code using OLD func CLI version
			// v1.20.2 has no middleware-version label and uses instance-compatible templates
			oldFuncVersion := "v1.20.2"
			repoDir, err = utils.InitializeRepoWithFunction(
				repoURL,
				username,
				password,
				"go",
				utils.WithCliVersion(oldFuncVersion))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			// Deploy function using the same OLD func CLI version
			out, err := utils.RunFuncDeploy(repoDir,
				utils.WithNamespace(functionNamespace),
				utils.WithDeployCliVersion(oldFuncVersion))
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

		It("should update the middleware and mark the function as ready", func() {
			// Get function metadata to retrieve the deployed function name
			funcMetadata, err := function.Metadata(repoDir)
			Expect(err).NotTo(HaveOccurred())
			deployedFunctionName := funcMetadata.Name

			// NOTE: We use skopeo to verify the middleware version because func describe
			// cannot access the image registry in our test environment.
			//
			// func describe calls MiddlewareVersion() which uses go-containerregistry's
			// remote.Get() to fetch the image manifest and read the middleware-version label.
			// However, the image reference is kind-registry:5000/..., where "kind-registry"
			// is a Docker container name, not a real hostname.
			//
			// Docker CLI can resolve container names on Docker networks (which is why
			// func deploy --registry kind-registry:5000 works for pushing images), but
			// go-containerregistry is not Docker-aware and tries regular DNS resolution,
			// which fails. The error is silently ignored in describers.
			//
			// We use skopeo with localhost:5001 (port-forward to the registry) to
			// directly inspect the OCI image labels and verify the middleware was updated.

			// Get initial image digest from func describe (deployed with v1.20.2)
			out, err := utils.RunFunc("describe", deployedFunctionName, "-n", functionNamespace, "-o", "yaml")
			Expect(err).NotTo(HaveOccurred())

			var initialInstance funcfn.Instance
			err = yaml.Unmarshal([]byte(out), &initialInstance)
			Expect(err).NotTo(HaveOccurred())

			initialImage := initialInstance.Image
			Expect(initialImage).NotTo(BeEmpty(), "Initial image should be available from func describe")
			_, _ = fmt.Fprintf(GinkgoWriter, "Initial image (deployed with v1.20.2): %s\n", initialImage)

			// Verify initial image has no middleware-version label (v1.20.2 doesn't set it)
			initialImageLocal := strings.Replace(initialImage, "kind-registry:5000", "localhost:5001", 1)
			// Remove tag if both tag and digest are present (skopeo doesn't support this format)
			if strings.Contains(initialImageLocal, "@") {
				atIndex := strings.Index(initialImageLocal, "@")
				slashIndex := strings.LastIndex(initialImageLocal[:atIndex], "/")
				if slashIndex != -1 {
					betweenSlashAndAt := initialImageLocal[slashIndex+1 : atIndex]
					if strings.Contains(betweenSlashAndAt, ":") {
						colonIndex := strings.Index(betweenSlashAndAt, ":")
						initialImageLocal = initialImageLocal[:slashIndex+1+colonIndex] + initialImageLocal[atIndex:]
					}
				}
			}
			cmd := exec.Command("skopeo",
				"inspect",
				"--tls-verify=false",
				"--no-tags",
				"docker://"+initialImageLocal)
			skopeoOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			var initialImageLabels struct {
				Labels map[string]string `json:"Labels"`
			}
			err = json.Unmarshal([]byte(skopeoOutput), &initialImageLabels)
			Expect(err).NotTo(HaveOccurred())

			initialMiddlewareVersion := initialImageLabels.Labels["middleware-version"]
			_, _ = fmt.Fprintf(GinkgoWriter, "Initial middleware-version label: '%s' (expected empty for v1.20.2)\n",
				initialMiddlewareVersion)

			// Create a Function resource
			fn := &functionsdevv1alpha1.Function{
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

			err = k8sClient.Create(ctx, fn)
			Expect(err).NotTo(HaveOccurred())

			functionName = fn.Name

			// Middleware update could take a bit longer therefore give more time
			Eventually(functionBecomesReady(functionName, functionNamespace), 6*time.Minute).Should(Succeed())

			// Verify middleware was actually updated by inspecting the new image
			out, err = utils.RunFunc("describe", deployedFunctionName, "-n", functionNamespace, "-o", "yaml")
			Expect(err).NotTo(HaveOccurred())

			var updatedInstance funcfn.Instance
			err = yaml.Unmarshal([]byte(out), &updatedInstance)
			Expect(err).NotTo(HaveOccurred())

			updatedImage := updatedInstance.Image
			Expect(updatedImage).NotTo(BeEmpty(), "Updated image should be available from func describe")
			_, _ = fmt.Fprintf(GinkgoWriter, "Updated image (redeployed by operator): %s\n", updatedImage)

			// Verify the image actually changed
			Expect(updatedImage).NotTo(Equal(initialImage), "Image should have changed after operator redeploy")

			// Verify updated image has middleware-version label set
			updatedImageLocal := strings.Replace(updatedImage, "kind-registry:5000", "localhost:5001", 1)
			// Remove tag if both tag and digest are present (skopeo doesn't support this format)
			// Format: registry/name:tag@digest -> registry/name@digest
			if strings.Contains(updatedImageLocal, "@") {
				atIndex := strings.Index(updatedImageLocal, "@")
				slashIndex := strings.LastIndex(updatedImageLocal[:atIndex], "/")
				if slashIndex != -1 {
					// Check if there's a colon between last slash and @
					betweenSlashAndAt := updatedImageLocal[slashIndex+1 : atIndex]
					if strings.Contains(betweenSlashAndAt, ":") {
						// Remove the :tag part
						colonIndex := strings.Index(betweenSlashAndAt, ":")
						updatedImageLocal = updatedImageLocal[:slashIndex+1+colonIndex] + updatedImageLocal[atIndex:]
					}
				}
			}
			cmd = exec.Command("skopeo", "inspect", "--tls-verify=false", "--no-tags", "docker://"+updatedImageLocal)
			skopeoOutput, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			var updatedImageLabels struct {
				Labels map[string]string `json:"Labels"`
			}
			err = json.Unmarshal([]byte(skopeoOutput), &updatedImageLabels)
			Expect(err).NotTo(HaveOccurred())

			updatedMiddlewareVersion := updatedImageLabels.Labels["middleware-version"]
			_, _ = fmt.Fprintf(GinkgoWriter, "Updated middleware-version label: '%s'\n", updatedMiddlewareVersion)

			// The operator should have set a middleware version
			Expect(updatedMiddlewareVersion).NotTo(BeEmpty(), "Operator should have deployed with middleware-version label set")

			Eventually(functionMiddlewareUpToDate(functionName, functionNamespace), 2*time.Minute).Should(Succeed())
		})
	})

	Context("when ConfigMap autoUpdateMiddleware setting changes", func() {
		const (
			operatorNamespace    = "func-operator-system"
			controllerConfigName = "func-operator-controller-config"
		)

		var repoURL string
		var repoDir string
		var functionName, functionNamespace string
		var originalConfigMapData map[string]string

		BeforeEach(func() {
			if os.Getenv("DEFAULT_DEPLOYER") == "keda" || os.Getenv("DEFAULT_DEPLOYER") == "raw" {
				Skip("Skipping middleware test for Keda & raw deployer, " +
					"as those are not supported on used CLI version (1.20.x) of this tests")
			}

			var err error

			// Save original ConfigMap data to restore later
			cm := &v1.ConfigMap{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred())
			originalConfigMapData = make(map[string]string)
			for k, v := range cm.Data {
				originalConfigMapData[k] = v
			}

			// Restore original ConfigMap data on cleanup
			DeferCleanup(func() {
				By("Restoring original ConfigMap data")
				cm := &v1.ConfigMap{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      controllerConfigName,
					Namespace: operatorNamespace,
				}, cm)
				Expect(err).NotTo(HaveOccurred())

				cm.Data = originalConfigMapData
				err = k8sClient.Update(ctx, cm)
				Expect(err).NotTo(HaveOccurred())
			})

			// Create repository provider resources with automatic cleanup
			username, password, _, cleanup, err := repoProvider.CreateRandomUser()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			_, repoURL, cleanup, err = repoProvider.CreateRandomRepo(username, false)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanup)

			functionNamespace, err = utils.GetTestNamespace()
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(cleanupNamespaces, functionNamespace)

			// Initialize repository with function code using OLD func CLI version
			// to ensure middleware will be outdated
			oldFuncVersion := "v1.20.2"
			repoDir, err = utils.InitializeRepoWithFunction(
				repoURL,
				username,
				password,
				"go",
				utils.WithCliVersion(oldFuncVersion))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(os.RemoveAll, repoDir)

			// Deploy function using the same OLD func CLI version
			out, err := utils.RunFuncDeploy(repoDir,
				utils.WithNamespace(functionNamespace),
				utils.WithDeployCliVersion(oldFuncVersion))
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

		It("should reconcile functions without explicit autoUpdateMiddleware when ConfigMap changes", func() {
			// Set ConfigMap to disable middleware updates
			By("Disabling middleware updates in ConfigMap")
			cm := &v1.ConfigMap{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred())

			cm.Data["autoUpdateMiddleware"] = "false"
			err = k8sClient.Update(ctx, cm)
			Expect(err).NotTo(HaveOccurred())

			// Wait for the ConfigMap update to propagate
			time.Sleep(2 * time.Second)

			// Create a Function resource WITHOUT autoUpdateMiddleware setting
			// (so it will use the operator default)
			fn := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-function-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: repoURL,
					},
					// NOTE: autoUpdateMiddleware is intentionally not set,
					// so it will use the operator default (currently "false")
				},
			}

			err = k8sClient.Create(ctx, fn)
			Expect(err).NotTo(HaveOccurred())
			functionName = fn.Name

			By("Waiting for Function to become ready with middleware updates disabled")
			Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())

			// Verify the MiddlewareUpToDate condition reflects that updates are disabled
			By("Verifying middleware update is skipped due to ConfigMap setting")
			Eventually(func(g Gomega) {
				fn := &functionsdevv1alpha1.Function{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      functionName,
					Namespace: functionNamespace,
				}, fn)
				g.Expect(err).NotTo(HaveOccurred())

				// Find the MiddlewareUpToDate condition
				for _, cond := range fn.Status.Conditions {
					if cond.Type == functionsdevv1alpha1.TypeMiddlewareUpToDate {
						// When middleware updates are disabled, the condition should be True
						// with reason "SkipMiddlewareUpdate"
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
						g.Expect(cond.Reason).To(Equal("SkipMiddlewareUpdate"))
						g.Expect(cond.Message).To(ContainSubstring("operator"))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "MiddlewareUpToDate condition not found")
			}, 1*time.Minute).Should(Succeed())

			// Get the initial image to compare later
			fnBefore := &functionsdevv1alpha1.Function{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      functionName,
				Namespace: functionNamespace,
			}, fnBefore)
			Expect(err).NotTo(HaveOccurred())
			imageBefore := fnBefore.Status.Deployment.Image

			// Now enable middleware updates in the ConfigMap
			By("Enabling middleware updates in ConfigMap")
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred())

			cm.Data["autoUpdateMiddleware"] = "true"
			err = k8sClient.Update(ctx, cm)
			Expect(err).NotTo(HaveOccurred())

			// The Function should be automatically reconciled and middleware should be updated
			By("Waiting for Function to be reconciled and middleware updated after ConfigMap change")
			Eventually(func(g Gomega) {
				fn := &functionsdevv1alpha1.Function{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      functionName,
					Namespace: functionNamespace,
				}, fn)
				g.Expect(err).NotTo(HaveOccurred())

				// The image should have changed (middleware was updated)
				g.Expect(fn.Status.Deployment.Image).NotTo(BeEmpty())
				g.Expect(fn.Status.Deployment.Image).NotTo(Equal(imageBefore),
					"Image should have changed after middleware update")

				// The MiddlewareUpToDate condition should now indicate middleware is up to date
				for _, cond := range fn.Status.Conditions {
					if cond.Type == functionsdevv1alpha1.TypeMiddlewareUpToDate {
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
						g.Expect(cond.Reason).To(Equal("UpToDate"))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "MiddlewareUpToDate condition not found")
			}, 5*time.Minute).Should(Succeed())

			Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())
		})

		It("should not reconcile functions with explicit autoUpdateMiddleware when ConfigMap changes", func() {
			// Set ConfigMap to enable middleware updates
			By("Setting ConfigMap to enable middleware updates")
			cm := &v1.ConfigMap{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred())

			cm.Data["autoUpdateMiddleware"] = "true"
			err = k8sClient.Update(ctx, cm)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(2 * time.Second)

			// Create a Function resource WITH explicit autoUpdateMiddleware=false
			// This should NOT be affected by ConfigMap changes
			fn := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-function-explicit-",
					Namespace:    functionNamespace,
				},
				Spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: repoURL,
					},
					// Explicitly set autoUpdateMiddleware
					AutoUpdateMiddleware: ptr.To(false),
				},
			}

			err = k8sClient.Create(ctx, fn)
			Expect(err).NotTo(HaveOccurred())
			functionName = fn.Name

			By("Waiting for Function to become ready")
			Eventually(functionBecomesReady(functionName, functionNamespace)).Should(Succeed())

			// Verify the MiddlewareUpToDate condition shows updates are skipped
			By("Verifying middleware update is skipped due to function setting")
			Eventually(func(g Gomega) {
				fn := &functionsdevv1alpha1.Function{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      functionName,
					Namespace: functionNamespace,
				}, fn)
				g.Expect(err).NotTo(HaveOccurred())

				for _, cond := range fn.Status.Conditions {
					if cond.Type == functionsdevv1alpha1.TypeMiddlewareUpToDate {
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
						g.Expect(cond.Reason).To(Equal("SkipMiddlewareUpdate"))
						// Message should indicate the source is the function spec
						g.Expect(cond.Message).To(ContainSubstring("function"))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "MiddlewareUpToDate condition not found")
			}, 1*time.Minute).Should(Succeed())

			// Get the current image
			fnBefore := &functionsdevv1alpha1.Function{}
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      functionName,
				Namespace: functionNamespace,
			}, fnBefore)
			Expect(err).NotTo(HaveOccurred())
			imageBefore := fnBefore.Status.Deployment.Image

			// Update ConfigMap to disable middleware updates
			// (opposite of the function's explicit setting, but should have no effect)
			By("Changing ConfigMap to disable middleware updates")
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      controllerConfigName,
				Namespace: operatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred())

			cm.Data["autoUpdateMiddleware"] = "false"
			err = k8sClient.Update(ctx, cm)
			Expect(err).NotTo(HaveOccurred())

			// Wait a bit to ensure controller has time to process ConfigMap change
			time.Sleep(10 * time.Second)

			// The Function should NOT be reconciled and image should not change
			By("Verifying Function was not reconciled after ConfigMap change")
			Consistently(func(g Gomega) {
				fn := &functionsdevv1alpha1.Function{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      functionName,
					Namespace: functionNamespace,
				}, fn)
				g.Expect(err).NotTo(HaveOccurred())

				// Image should not change
				g.Expect(fn.Status.Deployment.Image).To(Equal(imageBefore))

				// Condition should still show updates are skipped with "function" source
				for _, cond := range fn.Status.Conditions {
					if cond.Type == functionsdevv1alpha1.TypeMiddlewareUpToDate {
						g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
						g.Expect(cond.Reason).To(Equal("SkipMiddlewareUpdate"))
						g.Expect(cond.Message).To(ContainSubstring("function"))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "MiddlewareUpToDate condition not found")
			}, 30*time.Second, 5*time.Second).Should(Succeed())
		})
	})
})
