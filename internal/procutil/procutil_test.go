package procutil

import (
	"bytes"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func writeExec(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o755)
}

func TestDebugQuotedArgs(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "[]"},
		{[]string{}, "[]"},
		{[]string{"-i"}, `["-i"]`},
		{[]string{"-i", "-n"}, `["-i", "-n"]`},
		{[]string{"a b", "c"}, `["a b", "c"]`},
		{[]string{`quote"here`}, `["quote\"here"]`},
	}
	for _, c := range cases {
		if got := DebugQuotedArgs(c.in); got != c.want {
			t.Errorf("DebugQuotedArgs(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func lookOr(t *testing.T, prog string) {
	t.Helper()
	if _, err := exec.LookPath(prog); err != nil {
		t.Skipf("%s not available", prog)
	}
}

func TestFormatExitStatusNonZero(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only exit-code shell test")
	}
	lookOr(t, "sh")
	cmd := exec.Command("sh", "-c", "exit 1")
	_ = cmd.Run()
	got := FormatExitStatus(cmd.ProcessState)
	if got != "exit status: 1" {
		t.Errorf("FormatExitStatus = %q, want %q", got, "exit status: 1")
	}
}

func TestFormatExitStatusOther(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only exit-code shell test")
	}
	lookOr(t, "sh")
	cmd := exec.Command("sh", "-c", "exit 100")
	_ = cmd.Run()
	if got := FormatExitStatus(cmd.ProcessState); got != "exit status: 100" {
		t.Errorf("FormatExitStatus = %q, want %q", got, "exit status: 100")
	}
}

func TestFormatExitStatusSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only signal test")
	}
	lookOr(t, "sh")
	cmd := exec.Command("sh", "-c", "kill -KILL $$")
	_ = cmd.Run()
	got := FormatExitStatus(cmd.ProcessState)
	if got != "signal: 9 (SIGKILL)" {
		t.Errorf("FormatExitStatus = %q, want %q", got, "signal: 9 (SIGKILL)")
	}
}

func TestFormatExitStatusNil(t *testing.T) {
	if got := FormatExitStatus(nil); got != "exit status: unknown" {
		t.Errorf("FormatExitStatus(nil) = %q", got)
	}
}

func TestRunSuccess(t *testing.T) {
	lookOr(t, "true")
	if err := Run("true", nil, "", "noop"); err != nil {
		t.Fatalf("Run(true) error: %v", err)
	}
}

func TestRunNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	lookOr(t, "sh")
	err := Run("sh", []string{"-c", "exit 1"}, "", "mylabel")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "mylabel failed: `sh -c exit 1` exited with exit status: 1"
	if err.Error() != want {
		t.Errorf("Run error = %q, want %q", err.Error(), want)
	}
}

func TestRunSpawnFailure(t *testing.T) {
	err := Run("this-binary-does-not-exist-rpie", nil, "", "spawnstep")
	if err == nil {
		t.Fatal("expected spawn error")
	}
	if !strings.HasPrefix(err.Error(), "failed to spawn `this-binary-does-not-exist-rpie` for spawnstep: ") {
		t.Errorf("unexpected spawn error: %q", err.Error())
	}
}

func TestRunCapturedCapturesOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	lookOr(t, "sh")
	var sink bytes.Buffer
	err := Run("true", nil, "", "x")
	if err != nil {
		t.Fatal(err)
	}
	err = RunCaptured("sh", []string{"-c", "echo out; echo err 1>&2"}, "", "step", &sink)
	if err != nil {
		t.Fatalf("RunCaptured error: %v", err)
	}
	got := sink.String()
	if got != "out\nerr\n" {
		t.Errorf("sink = %q, want stdout-then-stderr %q", got, "out\nerr\n")
	}
}

func TestRunCapturedNonZeroAppendsAndReturns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	lookOr(t, "sh")
	var sink bytes.Buffer
	err := RunCaptured("sh", []string{"-c", "echo hi; exit 2"}, "", "make", &sink)
	if err == nil {
		t.Fatal("expected error")
	}
	wantMsg := "make failed: `sh -c echo hi; exit 2` exited with exit status: 2"
	if err.Error() != wantMsg {
		t.Errorf("error = %q, want %q", err.Error(), wantMsg)
	}
	wantSink := "hi\n" + wantMsg + "\n"
	if sink.String() != wantSink {
		t.Errorf("sink = %q, want %q", sink.String(), wantSink)
	}
}

func TestCaptureSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	lookOr(t, "sh")
	out, err := Capture("sh", []string{"-c", "echo '  trimmed  '"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "trimmed" {
		t.Errorf("Capture = %q, want %q", out, "trimmed")
	}
}

func TestCaptureNonZeroExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	lookOr(t, "sh")
	_, err := Capture("sh", []string{"-c", "echo boom 1>&2; exit 3"})
	if err == nil {
		t.Fatal("expected error")
	}
	want := "`sh -c echo boom 1>&2; exit 3` exited with exit status: 3: boom"
	if err.Error() != want {
		t.Errorf("Capture error = %q, want %q", err.Error(), want)
	}
}

func TestCaptureSpawnFailure(t *testing.T) {
	_, err := Capture("this-binary-does-not-exist-rpie", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.HasPrefix(err.Error(), "failed to spawn `this-binary-does-not-exist-rpie`: ") {
		t.Errorf("unexpected error: %q", err.Error())
	}
}

func TestRunRelativeProgramResolvedAgainstCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	script := dir + "/configure"
	if err := writeExec(script, "#!/bin/sh\nexit 0\n"); err != nil {
		t.Fatal(err)
	}
	if err := Run("./configure", nil, dir, "./configure"); err != nil {
		t.Fatalf("Run(./configure) resolved against cwd should succeed: %v", err)
	}
}

func TestRunRelativeProgramErrorKeepsVerbatim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only")
	}
	dir := t.TempDir()
	script := dir + "/configure"
	if err := writeExec(script, "#!/bin/sh\nexit 1\n"); err != nil {
		t.Fatal(err)
	}
	err := Run("./configure", []string{"--x"}, dir, "./configure")
	if err == nil {
		t.Fatal("expected error")
	}
	want := "./configure failed: `./configure --x` exited with exit status: 1"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
