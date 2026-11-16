//go:build !windows

package hcclient

import "os/exec"

func hideWindow(*exec.Cmd) {}
