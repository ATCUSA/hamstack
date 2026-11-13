package svc

import (
	"io"
	"os"
)

func stdio() (io.Reader, io.Writer, io.Writer) { return os.Stdin, os.Stdout, os.Stderr }
