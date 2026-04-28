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

package utils

import (
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strings"

	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
	"k8s.io/apimachinery/pkg/util/rand"
)

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(append(os.Environ(), cmd.Env...), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

func GetTestNamespace() (string, error) {
	name := fmt.Sprintf("test-%s", rand.String(8))
	cmd := exec.Command("kubectl", "create", "namespace", name)
	_, err := Run(cmd)

	if err != nil {
		return "", err
	}

	return name, nil
}

func DeferCleanupOnSuccess(args ...any) {
	DeferCleanup(func() {
		if !CurrentSpecReport().Failed() {
			fn := reflect.ValueOf(args[0])
			in := make([]reflect.Value, len(args)-1)
			for i, arg := range args[1:] {
				in[i] = reflect.ValueOf(arg)
			}
			fn.Call(in)
		}
	})
}
