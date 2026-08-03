//go:build !unix

package procutil

import "syscall"

var signalNames = map[syscall.Signal]string{}
