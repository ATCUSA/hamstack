package install

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Step is one action in an install plan: either a command to run, or a Go function.
type Step struct {
	Desc string
	Cmd  []string     // command to run (with sudo already prefixed if needed)
	Do   func() error // alternative to Cmd
	Dir  string       // working directory for Cmd; "" for the current one
	Env  []string     // extra environment for Cmd
	// Cleanup, if set, runs once every step is done or one has failed, provided this step was started:
	// a download step removes its temporary build folder here, so a failed build leaves nothing behind.
	Cleanup func()
}

// Display is the one-line form shown to the user.
func (s Step) Display() string {
	if len(s.Cmd) > 0 {
		return "$ " + strings.Join(s.Cmd, " ")
	}
	return s.Desc
}

// Options controls Run.
type Options struct {
	Notice string // shown before the plan, for disclaimers
	DryRun bool
	Yes    bool      // do not ask for confirmation
	In     io.Reader // confirmation input; os.Stdin if nil
	Out    io.Writer // os.Stdout if nil
}

// Run shows the steps, asks for confirmation (unless Yes or DryRun), then runs them in order.
func Run(steps []Step, o Options) error {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	if len(steps) == 0 {
		fmt.Fprintln(o.Out, "Nothing to install.")
		return nil
	}
	if o.Notice != "" {
		fmt.Fprintln(o.Out, o.Notice)
	}
	fmt.Fprintln(o.Out, "Plan:")
	for i, s := range steps {
		fmt.Fprintf(o.Out, "  %d. %s\n", i+1, s.Desc)
		if len(s.Cmd) > 0 {
			fmt.Fprintf(o.Out, "     %s\n", s.Display())
		}
	}
	if o.DryRun {
		fmt.Fprintln(o.Out, "\n(dry run: nothing was changed)")
		return nil
	}
	if !o.Yes && !Confirm(o.In, o.Out, "\nRun these steps?", false) {
		return fmt.Errorf("cancelled")
	}
	var cleanups []func()
	defer func() {
		for _, c := range slices.Backward(cleanups) {
			c()
		}
	}()
	for i, s := range steps {
		fmt.Fprintf(o.Out, "\n[%d/%d] %s\n", i+1, len(steps), s.Desc)
		if s.Cleanup != nil {
			cleanups = append(cleanups, s.Cleanup)
		}
		var err error
		switch {
		case s.Do != nil:
			err = s.Do()
		case len(s.Cmd) > 0:
			cmd := exec.Command(s.Cmd[0], s.Cmd[1:]...)
			cmd.Dir = s.Dir
			cmd.Env = append(os.Environ(), s.Env...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, o.Out, os.Stderr
			err = cmd.Run()
		}
		if err != nil {
			return fmt.Errorf("step %d (%s) failed: %w", i+1, s.Desc, err)
		}
	}
	return nil
}

// Confirm asks a yes/no question. An empty answer returns def.
func Confirm(in io.Reader, out io.Writer, question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	fmt.Fprintf(out, "%s %s ", question, hint)
	// Reuse a caller's buffered reader: a new one per question would swallow piped input.
	r, ok := in.(*bufio.Reader)
	if !ok {
		r = bufio.NewReader(in)
	}
	line, _ := r.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}
