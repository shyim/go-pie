//go:build e2e

package e2e_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

var binPath string

func TestMain(m *testing.M) {
	// Build the local gpie binary for Linux matching host architecture.
	tmpDir, err := os.MkdirTemp("", "gpie-e2e-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	binPath = filepath.Join(tmpDir, "gpie")
	cmd := exec.Command("go", "build", "-o", binPath, ".")
	cmd.Dir = "../.."
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			panic("go build failed: " + string(exitErr.Stderr))
		}
		panic(err)
	}

	os.Exit(m.Run())
}

type ExecResult struct {
	ExitCode int
	Stdout   string
}

func execInContainer(ctx context.Context, c testcontainers.Container, cmd []string) (ExecResult, error) {
	exitCode, reader, err := c.Exec(ctx, cmd)
	if err != nil {
		return ExecResult{}, err
	}
	var buf bytes.Buffer
	if reader != nil {
		_, _ = io.Copy(&buf, reader)
	}
	return ExecResult{
		ExitCode: exitCode,
		Stdout:   buf.String(),
	}, nil
}

func TestE2E_Info(t *testing.T) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: "php:8.4-cli",
		Cmd:   []string{"sleep", "infinity"},
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	defer func() {
		_ = ctr.Terminate(ctx)
	}()

	err = ctr.CopyFileToContainer(ctx, binPath, "/usr/local/bin/gpie", 0755)
	require.NoError(t, err)

	res, err := execInContainer(ctx, ctr, []string{"gpie", "info"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "Target PHP")
	assert.Contains(t, res.Stdout, "extension_dir")
}

func TestE2E_InstallAndUninstall(t *testing.T) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image: "php:8.4-cli",
		Cmd:   []string{"sleep", "infinity"},
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)
	defer func() {
		_ = ctr.Terminate(ctx)
	}()

	err = ctr.CopyFileToContainer(ctx, binPath, "/usr/local/bin/gpie", 0755)
	require.NoError(t, err)

	// Install asgrim/example-pie-extension.
	res, err := execInContainer(ctx, ctr, []string{"gpie", "install", "asgrim/example-pie-extension"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode, "gpie install failed. Output: %s", res.Stdout)
	assert.Contains(t, res.Stdout, "Install complete")

	// Verify loaded in php -m
	res, err = execInContainer(ctx, ctr, []string{"php", "-m"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	// Example pie extension registers example_pie_extension
	assert.True(t, strings.Contains(strings.ToLower(res.Stdout), "example_pie_extension"), "Extension not listed in php -m. Output: %s", res.Stdout)

	// Verify listed in gpie show.
	res, err = execInContainer(ctx, ctr, []string{"gpie", "show"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "example_pie_extension")
	assert.Contains(t, res.Stdout, "asgrim/example-pie-extension")

	// Uninstall
	res, err = execInContainer(ctx, ctr, []string{"gpie", "uninstall", "asgrim/example-pie-extension"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "uninstalled")

	// Verify not listed in gpie show anymore.
	res, err = execInContainer(ctx, ctr, []string{"gpie", "show"})
	require.NoError(t, err)
	assert.Equal(t, 0, res.ExitCode)
	assert.Contains(t, res.Stdout, "(none)")
}
