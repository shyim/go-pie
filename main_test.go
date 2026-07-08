package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var binPath string

func TestMain(m *testing.M) {
	// Build the binary
	tmpDir, err := os.MkdirTemp("", "rpie-test-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "go-pie")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func runCmd(args ...string) (string, string, error, int) {
	cmd := exec.Command(binPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			code = exitError.ExitCode()
		} else {
			code = -1
		}
	}
	return stdout.String(), stderr.String(), err, code
}

func TestShowsHelp(t *testing.T) {
	stdout, stderr, err, code := runCmd("--help")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "install")
	assert.Contains(t, stdout, "info")
}

func TestInfoReportsTargetPhp(t *testing.T) {
	// Requires a php binary on PATH; skip gracefully if absent.
	_, err := exec.LookPath("php")
	if err != nil {
		t.Skip("skipping: no php on PATH")
	}

	stdout, stderr, err, code := runCmd("info")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Target PHP")
	assert.Contains(t, stdout, "extension_dir")
}

func TestRejectsBadPackageName(t *testing.T) {
	_, stderr, err, code := runCmd("install", "not/a/valid/name")
	assert.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "vendor/name")
}

func TestRejectsUnknownBareExtensionName(t *testing.T) {
	_, stderr, err, code := runCmd("install", "definitelynotarealext")
	assert.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "Packagist name")
}

func TestRejectsConfigureOptionsWithMultiplePackages(t *testing.T) {
	_, stderr, err, code := runCmd("install", "a/b", "c/d", "--", "--enable-foo")
	assert.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "single extension")
}

func TestInstallRequiresAtLeastOnePackage(t *testing.T) {
	_, _, err, code := runCmd("install")
	assert.Error(t, err)
	assert.NotEqual(t, 0, code)
}
