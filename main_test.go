package main

import (
	"bytes"
	"context"
	"errors"
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
	tmpDir, err := os.MkdirTemp("", "gpie-test-")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(tmpDir, "gpie")
	cmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, ".")
	if err := cmd.Run(); err != nil {
		panic(err)
	}

	code := m.Run()
	if err := os.RemoveAll(tmpDir); err != nil {
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func runCmd(args ...string) (string, string, int, error) {
	cmd := exec.CommandContext(context.Background(), binPath, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			code = exitError.ExitCode()
		} else {
			code = -1
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestShowsHelp(t *testing.T) {
	stdout, stderr, code, err := runCmd("--help")
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

	stdout, stderr, code, err := runCmd("info")
	require.NoError(t, err, "stderr: %s", stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Target PHP")
	assert.Contains(t, stdout, "extension_dir")
}

func TestRejectsBadPackageName(t *testing.T) {
	_, stderr, code, err := runCmd("install", "not/a/valid/name")
	require.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "vendor/name")
}

func TestRejectsUnknownBareExtensionName(t *testing.T) {
	_, stderr, code, err := runCmd("install", "definitelynotarealext")
	require.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "Packagist name")
}

func TestRejectsConfigureOptionsWithMultiplePackages(t *testing.T) {
	_, stderr, code, err := runCmd("install", "a/b", "c/d", "--", "--enable-foo")
	require.Error(t, err)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "single extension")
}

func TestInstallRequiresAtLeastOnePackage(t *testing.T) {
	_, _, code, err := runCmd("install")
	require.Error(t, err)
	assert.NotEqual(t, 0, code)
}
