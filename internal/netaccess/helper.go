// Package netaccess contains the narrow, root-owned firewall helper installed
// only after the user reviews and confirms its installation plan.
package netaccess

import _ "embed"

//go:embed helper.py
var Helper string
