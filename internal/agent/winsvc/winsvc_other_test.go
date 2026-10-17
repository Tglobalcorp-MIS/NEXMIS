//go:build !windows

package winsvc

import (
	"errors"
	"testing"
	"time"
)

// Off Windows every management call must fail with ErrWindowsOnly. (On
// Windows these would really register services, so the test is not built.)
func TestManagementWindowsOnly(t *testing.T) {
	if IsService() {
		t.Skip("skip on service host")
	}
	if err := InstallService("n", "d", "desc", "exe"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("InstallService err = %v", err)
	}
	if err := StartService("n"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("StartService err = %v", err)
	}
	if err := StopService("n", time.Second); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("StopService err = %v", err)
	}
	if err := RemoveService("n"); !errors.Is(err, ErrWindowsOnly) {
		t.Fatalf("RemoveService err = %v", err)
	}
	if ServiceExists("n") {
		t.Fatal("ServiceExists should be false off Windows")
	}
}
