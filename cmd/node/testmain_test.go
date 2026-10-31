package main

import (
	"os"
	"testing"
)

// The tests in this package proxy to echo servers on loopback, which is exactly
// what the destination guard refuses. Tests of the guard turn it back on.
func TestMain(m *testing.M) {
	guardPrivateDestinations = false
	os.Exit(m.Run())
}

func withDestinationGuard(t *testing.T) {
	t.Helper()
	guardPrivateDestinations = true
	t.Cleanup(func() { guardPrivateDestinations = false })
}
