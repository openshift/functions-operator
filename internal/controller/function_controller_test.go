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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/functions-dev/func-operator/internal/funccli"
	"github.com/functions-dev/func-operator/internal/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"knative.dev/func/pkg/functions"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	functionsdevv1alpha1 "github.com/functions-dev/func-operator/api/v1alpha1"
)

var _ = Describe("Function Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"
		const resourceNamespace = "default"
		const functionName = "func-go"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: resourceNamespace,
		}

		defaultSpec := functionsdevv1alpha1.FunctionSpec{
			Repository: functionsdevv1alpha1.FunctionSpecRepository{
				URL:    "https://github.com/foo/bar",
				Branch: "my-branch",
			},
		}

		AfterEach(func() {
			resource := &functionsdevv1alpha1.Function{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance Function")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())

			By("Wait for resource to be deleted")
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, resource)
				return errors.IsNotFound(err)
			}).Should(BeTrue())
		})

		type reconcileTestCase struct {
			spec           functionsdevv1alpha1.FunctionSpec
			configureMocks func(*funccli.MockManager, *git.MockManager)
			statusChecks   func(*functionsdevv1alpha1.FunctionStatus)
			operatorConfig map[string]string
		}

		DescribeTable("should successfully reconcile the resource",
			func(tc reconcileTestCase) {
				By("creating the Function")
				err := createFunctionResource(resourceName, resourceNamespace, tc.spec)
				Expect(err).NotTo(HaveOccurred())

				By("Setting up mocks")
				funcCliManagerMock := funccli.NewMockManager(GinkgoT())
				gitManagerMock := git.NewMockManager(GinkgoT())
				tc.configureMocks(funcCliManagerMock, gitManagerMock)

				operatorNamespace := fmt.Sprintf("func-operator-%s", rand.String(6))

				By("Setting up the operator namespace")
				err = createNamespace(operatorNamespace)
				Expect(err).NotTo(HaveOccurred())

				By("Setting up the controller config")
				err = createControllerConfig(operatorNamespace, tc.operatorConfig)
				Expect(err).NotTo(HaveOccurred())

				By("Reconciling the created resource")
				controllerReconciler := &FunctionReconciler{
					Client:            k8sClient,
					Scheme:            k8sClient.Scheme(),
					Recorder:          &events.FakeRecorder{},
					FuncCliManager:    funcCliManagerMock,
					GitManager:        gitManagerMock,
					OperatorNamespace: operatorNamespace,
				}

				_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
					NamespacedName: typeNamespacedName,
				})
				Expect(err).NotTo(HaveOccurred())

				if tc.statusChecks != nil {
					f := &functionsdevv1alpha1.Function{}
					err := k8sClient.Get(ctx, typeNamespacedName, f)
					Expect(err).NotTo(HaveOccurred())

					tc.statusChecks(&f.Status)
				}
			},
			Entry("should deploy when middleware update required", reconcileTestCase{
				spec: defaultSpec,
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v2.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)
					funcMock.EXPECT().Deploy(mock.Anything, mock.Anything, resourceNamespace, funccli.DeployOptions{}).Return(nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "my-branch", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
			}),
			Entry("should skip deploy when middleware already up to date", reconcileTestCase{
				spec: defaultSpec,
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v1.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "my-branch", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
			}),
			Entry("should use main as default branch", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: "https://github.com/foo/bar",
					},
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v1.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "main", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
			}),

			Entry("should contain the git information in the status", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL:    "https://github.com/foo/bar",
						Branch: "my-branch",
					},
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v1.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "my-branch", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}, WithRepoOptionBranch("my-branch"), WithRepoOptionCommit("foobar")), nil)
				},
				statusChecks: func(status *functionsdevv1alpha1.FunctionStatus) {
					Expect(status.Git.ResolvedBranch).Should(Equal("my-branch"))
					Expect(status.Git.ObservedCommit).Should(Equal("foobar"))
				},
			}),
			Entry("should contain the deployment information in the status", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: "https://github.com/foo/bar",
					},
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
						Image: "my-image:v1.2.3",
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v1.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "main", mock.Anything).Return(createTmpGitRepo(functions.Function{
						Name:    "func-go",
						Runtime: "node",
						Deploy: functions.DeploySpec{
							Deployer: "keda",
						}}), nil)
				},
				statusChecks: func(status *functionsdevv1alpha1.FunctionStatus) {
					Expect(status.Deployment.Image).Should(Equal("my-image:v1.2.3"))
					Expect(status.Deployment.Deployer).Should(Equal("keda"))
					Expect(status.Deployment.Runtime).Should(Equal("node"))
				},
			}),
			Entry("should skip middleware update, when config is disabled", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: "https://github.com/foo/bar",
					},
					AutoUpdateMiddleware: nil,
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
						Image: "my-image:v1.2.3",
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v2.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)
					// no funcMock.EXPECT().Deploy call, as no redeploy expected!

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "main", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
				operatorConfig: map[string]string{
					"autoUpdateMiddleware": "false",
				},
			}),
			Entry("AutoUpdateMiddleware setting in function should take priority over operator config", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: "https://github.com/foo/bar",
					},
					AutoUpdateMiddleware: ptr.To(false),
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v1.0.0",
						},
						Image: "my-image:v1.2.3",
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v2.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v1.0.0", nil)
					// no funcMock.EXPECT().Deploy call, as no redeploy expected!

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "main", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
				operatorConfig: map[string]string{
					"autoUpdateMiddleware": "true",
				},
			}),

			Entry("Should populate the middleware information in the status", reconcileTestCase{
				spec: functionsdevv1alpha1.FunctionSpec{
					Repository: functionsdevv1alpha1.FunctionSpecRepository{
						URL: "https://github.com/foo/bar",
					},
					AutoUpdateMiddleware: ptr.To(true),
				},
				configureMocks: func(funcMock *funccli.MockManager, gitMock *git.MockManager) {
					funcMock.EXPECT().Describe(mock.Anything, functionName, resourceNamespace).Return(functions.Instance{
						Middleware: functions.Middleware{
							Version: "v2.0.0",
						},
						Image: "my-image:v1.2.3",
					}, nil)
					funcMock.EXPECT().GetLatestMiddlewareVersion(mock.Anything, mock.Anything, mock.Anything).Return("v2.0.0", nil)
					funcMock.EXPECT().GetMiddlewareVersion(mock.Anything, functionName, resourceNamespace).Return("v2.0.0", nil)

					gitMock.EXPECT().CloneRepository(mock.Anything, "https://github.com/foo/bar", "", "main", mock.Anything).Return(createTmpGitRepo(functions.Function{Name: "func-go"}), nil)
				},
				statusChecks: func(status *functionsdevv1alpha1.FunctionStatus) {
					Expect(status.Middleware.Current).Should(Equal("v2.0.0"))
					Expect(status.Middleware.AutoUpdate.Enabled).Should(BeTrue())
					Expect(status.Middleware.AutoUpdate.Source).Should(Equal("function"))
					Expect(status.Middleware.Available).Should(BeNil())
				},
			}),
		)
	})
})

func createControllerConfig(operatorNamespace string, config map[string]string) error {
	cm := v1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      controllerConfigName,
			Namespace: operatorNamespace,
		},
		Data: config,
	}

	return k8sClient.Create(ctx, &cm)
}

func createNamespace(ns string) error {
	return k8sClient.Create(ctx, &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	})
}

func createFunctionResource(name, namespace string, spec functionsdevv1alpha1.FunctionSpec) error {
	resource := functionsdevv1alpha1.Function{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: spec,
	}

	return k8sClient.Create(ctx, &resource)
}

type RepoOption func(*git.Repository)

func createTmpGitRepo(function functions.Function, repoOptions ...RepoOption) *git.Repository {
	tempDir, err := os.MkdirTemp("", function.Name)
	Expect(err).NotTo(HaveOccurred())

	funcYamlPath := filepath.Join(tempDir, "func.yaml")
	f, err := yaml.Marshal(function)
	Expect(err).NotTo(HaveOccurred())

	err = os.WriteFile(funcYamlPath, f, 0644)
	Expect(err).NotTo(HaveOccurred())

	opts := &git.Repository{
		CloneDir: tempDir,
	}

	for _, repoOption := range repoOptions {
		repoOption(opts)
	}

	return opts
}

func WithRepoOptionSubPath(subPath string) RepoOption {
	return func(repo *git.Repository) {
		repo.SubPath = subPath
	}
}

func WithRepoOptionBranch(branch string) RepoOption {
	return func(repo *git.Repository) {
		repo.Branch = branch
	}
}

func WithRepoOptionCommit(commit string) RepoOption {
	return func(repo *git.Repository) {
		repo.Commit = commit
	}
}
