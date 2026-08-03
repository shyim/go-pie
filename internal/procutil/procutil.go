// Package procutil provides subprocess helpers with uniform error formatting,
// mirroring the Rust util/process.rs module.
package procutil

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// resolveProgram mirrors Unix child-process semantics the Rust code relies on:
// a relative program containing a path separator is resolved against cwd, not
// the parent process working directory (which is what Go would otherwise do).
// The verbatim program string is preserved for error messages.
func resolveProgram(program, cwd string) string {
	if cwd == "" {
		return program
	}
	if strings.ContainsRune(program, '/') && !filepath.IsAbs(program) {
		return filepath.Join(cwd, program)
	}
	return program
}

// Run spawns program with args in cwd, inheriting stdio so build output streams
// to the user's terminal. Cancelling ctx kills the child process.
func Run(ctx context.Context, program string, args []string, cwd string, label string) error {
	cmd := exec.CommandContext(ctx, resolveProgram(program, cwd), args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to spawn `%s` for %s: %w", program, label, err)
	}
	err := cmd.Wait()
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		return fmt.Errorf("%s failed: `%s %s` exited with %s",
			label, program, strings.Join(args, " "), FormatExitStatus(cmd.ProcessState))
	}
	if err != nil {
		return fmt.Errorf("failed to spawn `%s` for %s: %w", program, label, err)
	}
	return nil
}

// RunCaptured captures stdout+stderr into sink (stdout appended first, then
// stderr). On non-zero exit it appends the failure line + "\n" to sink AND
// returns the same message (without trailing newline) as the error.
func RunCaptured(ctx context.Context, program string, args []string, cwd string, label string, sink *bytes.Buffer) error {
	cmd := exec.CommandContext(ctx, resolveProgram(program, cwd), args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to spawn `%s` for %s: %w", program, label, err)
	}
	err := cmd.Wait()

	sink.Write(stdout.Bytes())
	sink.Write(stderr.Bytes())

	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		msg := fmt.Sprintf("%s failed: `%s %s` exited with %s",
			label, program, strings.Join(args, " "), FormatExitStatus(cmd.ProcessState))
		sink.WriteString(msg)
		sink.WriteByte('\n')
		return fmt.Errorf("%s", msg)
	}
	if err != nil {
		return fmt.Errorf("failed to spawn `%s` for %s: %w", program, label, err)
	}
	return nil
}

// Capture runs program (inherited env, no cwd override), returns trimmed stdout.
func Capture(ctx context.Context, program string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, program, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to spawn `%s`: %w", program, err)
	}
	err := cmd.Wait()
	if cmd.ProcessState != nil && !cmd.ProcessState.Success() {
		return "", fmt.Errorf("`%s %s` exited with %s: %s",
			program, strings.Join(args, " "), FormatExitStatus(cmd.ProcessState),
			strings.TrimSpace(stderr.String()))
	}
	if err != nil {
		return "", fmt.Errorf("failed to spawn `%s`: %w", program, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// FormatExitStatus renders like Rust's ExitStatus Display:
// "exit status: 1", or "signal: 9 (SIGKILL)" for signal deaths.
func FormatExitStatus(state *os.ProcessState) string {
	if state == nil {
		return "exit status: unknown"
	}
	if ws, ok := state.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			sig := ws.Signal()
			return fmt.Sprintf("signal: %d (SIG%s)", int(sig), signalName(sig))
		}
	}
	return fmt.Sprintf("exit status: %d", state.ExitCode())
}

func signalName(sig syscall.Signal) string {
	if name, ok := signalNames[sig]; ok {
		return name
	}
	upper := strings.ToUpper(strings.TrimPrefix(sig.String(), "signal "))
	return strings.TrimPrefix(upper, "SIG")
}

// DebugQuotedArgs renders like Rust {:?} on a &[String]: ["-i", "-n"].
func DebugQuotedArgs(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}
