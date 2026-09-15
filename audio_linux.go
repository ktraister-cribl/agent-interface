//go:build linux

package main

import "os/exec"

func recordCmd() *exec.Cmd {
	cmd := exec.Command("arecord", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw", "-")
	cmd.Stdin = nil
	return cmd
}

func playCmd() *exec.Cmd {
	return exec.Command("aplay", "-r", "22050", "-f", "S16_LE", "-t", "raw", "-")
}
