package main

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// pidAlive reports whether a process exists and is not a zombie waiting to be collected.
func pidAlive(s string) bool {
	n, err := strconv.Atoi(s)
	if err != nil {
		return false
	}
	if syscall.Kill(n, 0) != nil {
		return false
	}
	state, err := readProcState(n)
	return err == nil && state != 'Z'
}

// readProcState is the state letter from /proc/PID/stat: it follows the last ")" because a program's name may
// itself hold spaces and brackets.
func readProcState(pid int) (byte, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, err
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 >= len(b) {
		return 0, fmt.Errorf("unreadable stat")
	}
	return b[i+2], nil
}
