package utils

import (
	"os"
	"os/exec"
	"sync"
)

func OperatorSdkRun(command string, args ...string) (string, error) {
	cmd := exec.Command(operatorSdkBinary(), append([]string{command}, args...)...)

	return Run(cmd)
}

var (
	operatorSdkBinaryPath    string
	operatorSdkBinaryGetOnce sync.Once
)

func operatorSdkBinary() string {
	operatorSdkBinaryGetOnce.Do(func() {
		operatorSdkBinaryPath = os.Getenv("OPERATOR_SDK")
		if operatorSdkBinaryPath == "" {
			operatorSdkBinaryPath = "operator-sdk"
		}
	})

	return operatorSdkBinaryPath
}
