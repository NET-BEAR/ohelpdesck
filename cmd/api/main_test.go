package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

func TestMissingConfig(t *testing.T) {
	if os.Getenv("TEST_ENTRYPOINT") == "1" {
		main()
		return
	}
	c := exec.Command(os.Args[0], "-test.run=TestMissingConfig")
	c.Env = append(os.Environ(), "TEST_ENTRYPOINT=1", "ENVIRONMENT=production", "DATABASE_URL=", "REDIS_URL=redis://localhost:6379", "S3_ENDPOINT=localhost:9000", "S3_BUCKET=test", "S3_ACCESS_KEY=test-access", "S3_SECRET_KEY=test-secret")
	out, e := c.CombinedOutput()
	if e == nil || !bytes.Contains(out, []byte("DATABASE_URL")) {
		t.Fatalf("expected safe missing-field message; got %s", out)
	}
}
