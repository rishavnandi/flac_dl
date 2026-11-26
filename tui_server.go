//go:build vercel || server

package main

import "errors"

func runTUI() error {
	return errors.New("the server build does not include the terminal UI")
}
