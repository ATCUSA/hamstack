package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ATCUSA/hamstack/internal/config"
	"github.com/ATCUSA/hamstack/internal/rigctl"
)

// The transmit watchdog. An HF modem keys the radio through Pat and rigctld; if the modem hangs mid-transmission
// (seen on the Pi: ardopcf stuck after PTT-TRUE, the transmitter held for minutes, Pat's abort no help), nothing
// would let go. While a rig's HF modem runs, hamstack watches the radio's PTT through rigctld, and if it stays keyed
// longer than any over of these modems takes, it unkeys the radio and restarts the modem. Other programs that key the
// radio (WSJT-X, Fldigi) stop the HF modem first, so a key held while it runs is the modem's.

// txLimit is the longest the radio may stay keyed while an HF modem runs: ARDOP, Mercury and Data2G send bursts of a
// few seconds, then listen.
const txLimit = 30 * time.Second

type pttGuard struct {
	limit, every time.Duration
	read         func() (bool, error) // is the radio keyed?
	unkey        func() error
	trip         func(held time.Duration) // after an unkey: say so, restart the modem
	now          func() time.Time
	sleep        func(time.Duration)
}

// run watches until ctx ends. An unreadable state counts as not keyed (rig control may be restarting).
func (g pttGuard) run(ctx context.Context) {
	var since time.Time
	for ctx.Err() == nil {
		keyed, err := g.read()
		switch {
		case err != nil || !keyed:
			since = time.Time{}
		case since.IsZero():
			since = g.now()
		case g.now().Sub(since) > g.limit:
			held := g.now().Sub(since)
			_ = g.unkey()
			since = time.Time{}
			g.trip(held)
		}
		g.sleep(g.every)
	}
}

// superviseModem runs an HF modem as a child of this process, with the transmit watchdog, instead of replacing
// this process with it. Stop signals are passed on, and a modem that ends after one has stopped cleanly; otherwise
// the exit status is the modem's. When the watchdog trips, the
// modem is stopped and this returns an error, so systemd starts it again (Restart=on-failure).
func superviseModem(rig config.Rig, kind, path string, argv []string) error {
	if !rig.NoCAT {
		// The radio settings check: wrong receive-side settings (AGC OFF) fixed before the modem listens, and again
		// after each band change.
		radioAutoFix(rig, radioClient(rig), os.Stderr)
		stopWatch := make(chan struct{})
		defer close(stopWatch)
		go watchBands(rig, radioClient(rig), os.Stderr, bandWatchEvery, stopWatch)
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Args = argv
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tripped := make(chan time.Duration, 1)
	if !rig.NoCAT {
		addr := "127.0.0.1:" + strconv.Itoa(rig.RigPort)
		g := pttGuard{
			limit: txLimit, every: time.Second, now: time.Now, sleep: time.Sleep,
			read: func() (bool, error) {
				c := rigctl.New(addr)
				defer c.Close()
				return c.PTT()
			},
			unkey: func() error {
				c := rigctl.New(addr)
				defer c.Close()
				_, err := c.Do("T 0")
				return err
			},
			trip: func(held time.Duration) {
				select {
				case tripped <- held:
				default:
				}
			},
		}
		go g.run(ctx)
	}
	return superviseLoop(rig, kind, done, sigs, tripped, cmd.Process.Signal, func() { _ = cmd.Process.Kill() })
}

// superviseLoop waits for the modem to end, passing stop signals on and acting on the watchdog. A modem asked to stop
// ends with a status of its own (xara: 130, the code for an interrupt), which is not a failure: when the stop came
// from systemd (or a person), the service has stopped cleanly and must not show as failed.
func superviseLoop(rig config.Rig, kind string, done <-chan error, sigs <-chan os.Signal, tripped <-chan time.Duration,
	signal func(os.Signal) error, kill func()) error {
	stopping := false
	for {
		select {
		case err := <-done:
			if stopping {
				return nil
			}
			return err
		case s := <-sigs:
			stopping = true
			_ = signal(s)
		case held := <-tripped:
			fmt.Fprintf(os.Stderr, "hamstack: rig %s: the radio stayed keyed for %s while %s ran: unkeyed it, and restarting %s (a modem holding the transmitter has hung)\n",
				rig.ID, held.Round(time.Second), kind, kind)
			_ = signal(syscall.SIGTERM)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				kill()
				<-done
			}
			return fmt.Errorf("rig %s: %s held the transmitter for %s and was stopped", rig.ID, kind, held.Round(time.Second))
		}
	}
}
