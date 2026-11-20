package term

import (
	"strconv"
	"strings"
)

// ProgressState is the OSC 9;4 progress state.
type ProgressState uint8

const (
	ProgressHidden ProgressState = iota
	ProgressNormal
	ProgressError
	ProgressIndeterminate
	ProgressPaused
)

type Progress struct {
	State   ProgressState
	Percent uint8
}

func parseProgress(s string) (Progress, bool) {
	s, ok := strings.CutPrefix(s, "4;")
	if !ok {
		return Progress{}, false
	}
	state, value, _ := strings.Cut(s, ";")
	n, err := strconv.Atoi(state)
	if err != nil || n < 0 || n > int(ProgressPaused) {
		return Progress{}, false
	}
	var percent int
	if value != "" {
		percent, err = strconv.Atoi(value)
		if err != nil {
			return Progress{}, false
		}
	}
	return Progress{State: ProgressState(n), Percent: uint8(max(0, min(100, percent)))}, true
}
