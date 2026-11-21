//go:build !darwin || ios

package main

import "errors"

func supportsClipboardPNGCompression() bool { return false }

func readClipboardPNGSnapshot() ([]byte, int64, error) {
	return nil, 0, errors.ErrUnsupported
}

func writeClipboardPNGIfUnchanged([]byte, int64) error { return errors.ErrUnsupported }
