package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
)

// A modem hung with the radio keyed (seen on the Pi: ardopcf stuck after PTT-TRUE, transmitter held for minutes).
// The guard must unkey after the limit and say so, whatever the modem is doing.
func TestPTTGuardUnkeysAStuckTransmitter(t *testing.T) {
	now := time.Unix(0, 0)
	keyed := true
	var unkeys, trips int
	g := pttGuard{
		limit: 30 * time.Second, every: time.Second,
		now:   func() time.Time { return now },
		sleep: func(time.Duration) { now = now.Add(time.Second) },
		read:  func() (bool, error) { return keyed, nil },
		unkey: func() error { unkeys++; keyed = false; return nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	g.trip = func(held time.Duration) { trips++; cancel() }
	g.run(ctx)
	if unkeys != 1 || trips != 1 {
		t.Fatalf("unkeys %d trips %d", unkeys, trips)
	}
	if now.Sub(time.Unix(0, 0)) > 32*time.Second {
		t.Fatalf("held for %v: the limit is 30 s", now.Sub(time.Unix(0, 0)))
	}
}

func TestPTTGuardLeavesShortOversAlone(t *testing.T) {
	now := time.Unix(0, 0)
	tick := 0
	var unkeys int
	ctx, cancel := context.WithCancel(context.Background())
	g := pttGuard{
		limit: 30 * time.Second, every: time.Second,
		now:   func() time.Time { return now },
		sleep: func(time.Duration) { now = now.Add(time.Second) },
		// an ARQ session: 5 s keyed, 3 s listening, for two minutes
		read: func() (bool, error) {
			tick++
			if tick > 120 {
				cancel()
			}
			return tick%8 < 5, nil
		},
		unkey: func() error { unkeys++; return nil },
		trip:  func(time.Duration) {},
	}
	g.run(ctx)
	if unkeys != 0 {
		t.Fatalf("a normal session was cut %d times", unkeys)
	}
}

func TestPTTGuardForgetsWhenItCannotRead(t *testing.T) {
	now := time.Unix(0, 0)
	tick := 0
	var unkeys int
	ctx, cancel := context.WithCancel(context.Background())
	g := pttGuard{
		limit: 30 * time.Second, every: time.Second,
		now:   func() time.Time { return now },
		sleep: func(time.Duration) { now = now.Add(time.Second) },
		read: func() (bool, error) {
			tick++
			if tick > 100 {
				cancel()
			}
			if tick%20 == 0 { // rig control restarting now and then
				return false, errors.New("connection refused")
			}
			return true, nil
		},
		unkey: func() error { unkeys++; return nil },
		trip:  func(time.Duration) {},
	}
	g.run(ctx)
	if unkeys != 0 {
		t.Fatalf("unknown is not keyed: %d", unkeys)
	}
}

// Stopping the service must not leave it "failed": xara ends with status 130 when asked to stop.
func TestAModemStoppedOnPurposeIsNotAFailure(t *testing.T) {
	rig := config.Rig{ID: "hf"}
	done := make(chan error, 1)
	sigs := make(chan os.Signal, 1)
	var got os.Signal
	sigs <- syscall.SIGTERM
	err := superviseLoop(rig, "xara", done, sigs, nil, func(s os.Signal) error {
		got = s
		done <- errors.New("exit status 130")
		return nil
	}, func() {})
	if err != nil || got != syscall.SIGTERM {
		t.Fatalf("a stop passed on and obeyed: %v (signal %v)", err, got)
	}
	// Ending by itself is still the modem's own failure, for systemd to restart.
	done <- errors.New("exit status 1")
	if err := superviseLoop(rig, "xara", done, make(chan os.Signal), nil, func(os.Signal) error { return nil }, func() {}); err == nil {
		t.Fatal("a modem that dies on its own must fail the service")
	}
}
