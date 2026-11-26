//go:build vercel

package main

import "errors"

func runTUI() error {
	return errors.New("the Vercel build does not include the terminal UI")
}
