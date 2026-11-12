package browse

import "syscall"

// launchAttr makes a launched Chrome die with the program that launched it: a test run killed
// (a timeout, Ctrl-C) never leaves its Chromes behind holding memory, the way hundreds of them
// once starved a machine until its low-memory killer took the renderers of the tests running.
func launchAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
