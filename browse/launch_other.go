//go:build !linux

package browse

import "syscall"

// launchAttr is nothing where the system cannot tie a child's life to its parent's; `scl kill`
// sweeps the Chromes a program left there.
func launchAttr() *syscall.SysProcAttr {
	return nil
}
