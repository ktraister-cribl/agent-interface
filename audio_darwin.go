//go:build darwin

package main

import "os/exec"

func recordCmd() *exec.Cmd {
	cmd := exec.Command("rec", "-q", "-r", "16000", "-c", "1", "-b", "16", "-e", "signed-integer", "-t", "raw", "-")
	cmd.Stdin = nil
	return cmd
}

func playCmd() *exec.Cmd {
	return exec.Command("play", "-q", "-r", "22050", "-b", "16", "-e", "signed-integer", "-c", "1", "-t", "raw", "-")
}
