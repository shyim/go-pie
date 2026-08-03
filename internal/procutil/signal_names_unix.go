//go:build unix

package procutil

import "syscall"

var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP:  "HUP",
	syscall.SIGINT:  "INT",
	syscall.SIGQUIT: "QUIT",
	syscall.SIGILL:  "ILL",
	syscall.SIGTRAP: "TRAP",
	syscall.SIGABRT: "ABRT",
	syscall.SIGBUS:  "BUS",
	syscall.SIGFPE:  "FPE",
	syscall.SIGKILL: "KILL",
	syscall.SIGUSR1: "USR1",
	syscall.SIGSEGV: "SEGV",
	syscall.SIGUSR2: "USR2",
	syscall.SIGPIPE: "PIPE",
	syscall.SIGALRM: "ALRM",
	syscall.SIGTERM: "TERM",
}
