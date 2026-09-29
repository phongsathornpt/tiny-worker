package main

import "errors"

// errExit2 signals a usage error (exit code 2) after the message is printed.
var errExit2 = errors.New("usage")

// errHelpShown signals that -h/--help already printed the flag usage; the
// command should exit 0 without another message.
var errHelpShown = errors.New("help shown")
