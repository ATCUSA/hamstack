package web

import "fmt"

// SudoNeeded is returned when an action needs administrator rights and the dashboard has no passwordless way to get
// them. It is not a failure: the page shows the person why, what exactly would run as root, and asks for the
// password, or lets them cancel and run it in a terminal. The password comes back in ManagementRequest.Secret.
type SudoNeeded struct {
	User     string   `json:"user"`               // the account whose password is wanted
	Why      string   `json:"why"`                // in words, what needs it
	Commands []string `json:"commands"`           // what will run as root
	Terminal string   `json:"terminal,omitempty"` // what to run in a terminal instead
}

func (e *SudoNeeded) Error() string { return e.Why + " (the administrator password is needed)" }

// SudoRejected is a password that did not work, or too many that did not (the account would lock).
type SudoRejected struct {
	Message string
	Locked  bool
}

func (e *SudoRejected) Error() string { return e.Message }

func (e *SudoRejected) code() string {
	if e.Locked {
		return "sudo-locked"
	}
	return "sudo-rejected"
}

var _ = fmt.Sprint
