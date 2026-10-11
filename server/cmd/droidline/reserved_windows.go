//go:build windows

package main

import "os/exec"

// excludedRanges lists the ports Windows keeps for itself, often for Hyper-V, WSL or Docker. proto is "tcp" or "udp".
func excludedRanges(proto string) ([]portRange, error) {
	out, err := exec.Command("netsh", "interface", "ipv4", "show", "excludedportrange", "protocol="+proto).Output()
	if err != nil {
		return nil, err
	}
	return parseExcluded(string(out)), nil
}
