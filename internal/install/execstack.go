package install

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
)

// gfortran builds a small piece of code on the stack (a trampoline) when Fortran passes an internal procedure as an
// argument, and jumps to it. That only works when the program's stack is executable, which the PT_GNU_STACK entry of
// its ELF header says. WSJT-X's decoder, jt9, does this as soon as it has a signal to decode. A build that marks jt9's
// stack non-executable gives a jt9 that runs and decodes noise but crashes (SIGSEGV, the program counter on the stack)
// on the first real signal. Seen with WSJT-X Improved 3.2.1 built from source on a Raspberry Pi 5 (Debian 13, GCC 14);
// Debian's own wsjtx-improved package marks jt9, and only jt9, executable-stack.

const (
	ptGNUStack = 0x6474e551
	pfX        = 1
)

// stackFlags finds the PT_GNU_STACK program header of the ELF file b: the offset of its flags word, and the flags.
func stackFlags(b []byte) (off int, flags uint32, err error) {
	if len(b) < 0x40 || string(b[:4]) != "\x7fELF" {
		return 0, 0, fmt.Errorf("not an ELF program")
	}
	var bo binary.ByteOrder
	switch b[5] {
	case 1:
		bo = binary.LittleEndian
	case 2:
		bo = binary.BigEndian
	default:
		return 0, 0, fmt.Errorf("unknown ELF byte order")
	}
	var phoff, phentsize, phnum, flagsAt int
	switch b[4] {
	case 2: // 64-bit: p_flags follows p_type
		phoff, phentsize, phnum, flagsAt = int(bo.Uint64(b[0x20:])), int(bo.Uint16(b[0x36:])), int(bo.Uint16(b[0x38:])), 4
	case 1: // 32-bit: p_flags comes after six other words
		phoff, phentsize, phnum, flagsAt = int(bo.Uint32(b[0x1c:])), int(bo.Uint16(b[0x2a:])), int(bo.Uint16(b[0x2c:])), 24
	default:
		return 0, 0, fmt.Errorf("unknown ELF class")
	}
	for i := 0; i < phnum; i++ {
		o := phoff + i*phentsize
		if o < 0 || o+flagsAt+4 > len(b) {
			return 0, 0, fmt.Errorf("truncated ELF program headers")
		}
		if bo.Uint32(b[o:]) == ptGNUStack {
			return o + flagsAt, bo.Uint32(b[o+flagsAt:]), nil
		}
	}
	return 0, 0, fmt.Errorf("no PT_GNU_STACK entry")
}

// NeedsExecStack reports whether the program at path is marked with a non-executable stack, and so would crash if
// it needs one. A program with no stack entry at all gets an executable stack from the kernel: false.
func NeedsExecStack(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	_, flags, err := stackFlags(b)
	if err != nil {
		return false, nil
	}
	return flags&pfX == 0, nil
}

// MarkExecStack marks the program at path as needing an executable stack, as `execstack -s` does. It writes a new
// file beside it and renames it into place, so a running copy keeps its old file. It does nothing if already marked.
func MarkExecStack(path string) (changed bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	off, flags, err := stackFlags(b)
	if err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	if flags&pfX != 0 {
		return false, nil
	}
	var bo binary.ByteOrder = binary.LittleEndian
	if b[5] == 2 {
		bo = binary.BigEndian
	}
	bo.PutUint32(b[off:], flags|pfX)
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Chmod(tmp.Name(), fi.Mode().Perm()); err != nil {
		return false, err
	}
	return true, os.Rename(tmp.Name(), path)
}
