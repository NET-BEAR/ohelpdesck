package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

func TestBootstrapRequiresRuntimePassword(t *testing.T) {
	if os.Getenv("TEST_BOOTSTRAP_ENTRY") == "1" {
		main()
		return
	}
	command := exec.Command(os.Args[0], "-test.run=TestBootstrapRequiresRuntimePassword")
	command.Env = append(os.Environ(), "TEST_BOOTSTRAP_ENTRY=1", "INITIAL_ADMIN_PASSWORD=")
	output, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("password is required")) {
		t.Fatalf("unsafe bootstrap failure: %s", output)
	}
}
