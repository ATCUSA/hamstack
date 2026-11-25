package main

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	afterProgramsChanged = func() {} // a finished job must not look up real versions in a test
	os.Exit(m.Run())
}

// A release build stamps the version with -ldflags; a plain build must still say something useful.
func TestVersionDefaultsToDev(t *testing.T) {
	if version == "" {
		t.Fatal("version must never be empty")
	}
}
