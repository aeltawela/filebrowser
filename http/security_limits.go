package fbhttp

import (
	"errors"
)

// Named bounds for user-controlled input sizes (W3 hardening).
//
// These are package constants, not configuration: the fork keeps
// runtime-facing options aligned with docs/CLI, and adding settings fields is
// a follow-up. The values below are conservative defaults applied
// unconditionally.
//
// GHSA-448h-jr2h-3vhp — subtitle conversion held several copies of the input
// in memory, so it is capped well below process memory.
const (
	// SubtitleMaxBytes is the largest subtitle file accepted for conversion.
	// Larger inputs are rejected with 413 before they are read.
	SubtitleMaxBytes = 4 << 20 // 4 MiB

	// SubtitleMaxOutputBytes caps the rendered WebVTT output. Conversion can
	// expand the input, so the output bound is larger than the input bound.
	SubtitleMaxOutputBytes = 8 << 20 // 8 MiB
)

// errSubtitleTooLarge / errSubtitleOutputTooLarge mark 413 rejections on the
// subtitle path.
var (
	errSubtitleTooLarge       = errors.New("subtitle exceeds size limit")
	errSubtitleOutputTooLarge = errors.New("subtitle output exceeds size limit")
)
