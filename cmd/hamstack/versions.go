package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/ATCUSA/hamstack/internal/catalog"
	"github.com/ATCUSA/hamstack/internal/distro"
	"github.com/ATCUSA/hamstack/internal/versions"
)

type row struct {
	pkg       catalog.Package
	installed *versions.Installed
	latest    string
	latestErr error
}

func runVersions(offline bool) int {
	d := distro.Detect()
	fmt.Printf("System: %s (family %s, package manager %q)\n\n", d.Name, d.Family, d.Manager)

	rows := make([]row, len(catalog.All))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i, p := range catalog.All {
		rows[i].pkg = p
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows[i].installed = versions.FindInstalled(p)
			if !offline {
				rows[i].latest, rows[i].latestErr = versions.Latest(ctx, p)
			}
		}()
	}
	wg.Wait()

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PACKAGE\tINSTALLED\tLATEST\tSTATUS\tINSTALLED VIA")
	outdated := 0
	for _, r := range rows {
		inst, via := "-", "-"
		if r.installed != nil {
			inst, via = r.installed.Version, r.installed.Origin
			if inst == "" {
				inst = "unknown"
			}
		}
		latest := r.latest
		switch {
		case offline:
			latest = "-"
		case errors.Is(r.latestErr, versions.ErrNoUpstream):
			latest = "-"
		case r.latestErr != nil:
			latest = "error"
		}
		status := "-"
		switch {
		case r.installed == nil:
			status = "not installed"
		case offline || r.latestErr != nil:
			status = "unchecked"
			if errors.Is(r.latestErr, versions.ErrNoUpstream) {
				status = "installed"
			}
		case r.installed.Version == "":
			status = "version unknown"
		case versions.Compare(r.installed.Version, r.latest) < 0:
			status, outdated = "update available", outdated+1
		default:
			status = "up to date"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.pkg.Name, inst, latest, status, via)
	}
	tw.Flush()
	for _, r := range rows {
		if r.latestErr != nil {
			if !errors.Is(r.latestErr, versions.ErrNoUpstream) {
				fmt.Fprintf(os.Stderr, "note: %s: could not check upstream: %v\n", r.pkg.Name, r.latestErr)
			}
		}
	}
	if outdated > 0 {
		fmt.Printf("\n%d update(s) available. 'Latest' is the upstream release; a distro package may lag it.\n", outdated)
	}
	return 0
}
