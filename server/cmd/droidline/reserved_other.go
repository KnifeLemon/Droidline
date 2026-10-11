//go:build !windows

package main

import "errors"

// excludedRanges has nothing to report outside Windows.
func excludedRanges(string) ([]portRange, error) { return nil, errors.ErrUnsupported }
