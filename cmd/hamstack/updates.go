package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/ATCUSA/hamstack/internal/updates"
)

func cmdUpdates(args []string) error {
	if len(args) > 0 && args[0] == "self" {
		return updateSelf(args[1:])
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	snapshot, err := updates.Check(ctx, true)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PROGRAM\tINSTALLED\tLATEST\tSTATUS")
	for _, p := range snapshot.Programs {
		if p.Status == "not installed" {
			continue
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Name, orDash(p.Installed), orDash(p.Latest), p.Status)
		if p.Error != "" {
			fmt.Fprintf(os.Stderr, "%s: %s\n", p.Name, p.Error)
		}
	}
	tw.Flush()
	fmt.Println("Review app updates: hamstack update --dry-run. Update hamstack itself: hamstack updates self.")
	return err
}
