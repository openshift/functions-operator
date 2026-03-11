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

var _ = Describe("Operator", Ordered, func() {

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("with a deployed function", func() {
		var tempDir string
		var functionName, functionNamespace string

		BeforeEach(func() {
			var err error
			// deploy function
			tempDir = fmt.Sprintf("%s/func-operator-e2e-%s", os.TempDir(), rand.String(10))
			Expect(err).NotTo(HaveOccurred())

			cmd := exec.Command("git", "clone", "https://github.com/creydr/func-go-hello-world", tempDir)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("func", "deploy",
				"--path", tempDir,
				"--registry", registry,
				"--registry-insecure", strconv.FormatBool(registryInsecure))
			out, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			_, _ = fmt.Fprint(GinkgoWriter, out)
		})

		AfterEach(func() {
			specReport := CurrentSpecReport()
			if specReport.Failed() {
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
				cmd := exec.Command("kubectl", "logs", "-l", "control-plane=controller-manager", "-n", namespace)
				controllerLogs, err := utils.Run(cmd)
				if err == nil {
					_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
				} else {
					_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
				}
			}

			if tempDir != "" {
				cmd := exec.Command("func", "delete", "--path", tempDir)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
			}

			cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should mark the function as ready", func() {
			// Create a Function resource
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-function-",
					Namespace:    "default",
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

			functionName = function.Name
			functionNamespace = function.Namespace

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

			// redeploy could take a bit longer therefore give a bit more time
			Eventually(funcBecomeReady, 6*time.Minute).Should(Succeed())
		})
	})
	Context("with a not yet deployed function", func() {
		var functionName, functionNamespace string

		AfterEach(func() {
			cmd := exec.Command("kubectl", "delete", "function", functionName, "-n", functionNamespace, "--ignore-not-found")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should mark the function as not ready", func() {
			// Create a Function resource
			function := &functionsdevv1alpha1.Function{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "my-undeployed-function-",
					Namespace:    "default",
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

			functionName = function.Name
			functionNamespace = function.Namespace

			funcBecomeReady := func(g Gomega) {
				fn := &functionsdevv1alpha1.Function{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: function.Name, Namespace: function.Namespace}, fn)
				g.Expect(err).NotTo(HaveOccurred())

				for _, cond := range fn.Status.Conditions {
					if cond.Type == functionsdevv1alpha1.TypeDeployed {
						g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
						g.Expect(cond.Reason).To(Equal("NotDeployed"))
						g.Expect(cond.Message).To(ContainSubstring("Function not deployed yet"))
						return
					}
				}
				g.Expect(false).To(BeTrue(), "Deployed condition not found")
			}

			Eventually(funcBecomeReady, 2*time.Minute).Should(Succeed())
		})
	})
})
