package main

import "testing"

// A release build stamps the version with -ldflags; a plain build must still say something useful.
func TestVersionDefaultsToDev(t *testing.T) {
	if version == "" {
		t.Fatal("version must never be empty")
	}
}
