package fbhttp

import (
	"errors"
	"os"
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

// GHSA-8q5j-8wcr-8v2v — archive walks blocked forever on FIFOs and had no
// bound on entry count or total bytes.
const (
	// ArchiveMaxEntries caps the number of entries collected during a single
	// archive walk. Exceeding it aborts the request with 413.
	ArchiveMaxEntries = 10000

	// ArchiveMaxTotalBytes caps the summed logical size of archived regular
	// files, enforced during the walk before the archiver runs.
	ArchiveMaxTotalBytes = int64(1 << 30) // 1 GiB
)

// ErrArchiveTooLarge reports that an archive walk exceeded ArchiveMaxEntries
// or ArchiveMaxTotalBytes. Handlers map it to 413.
var ErrArchiveTooLarge = errors.New("archive exceeds entry or byte limit")

// archiveBudget enforces ArchiveMaxEntries/ArchiveMaxTotalBytes while walking.
type archiveBudget struct {
	entries int
	bytes   int64
}

func (b *archiveBudget) add(isDir bool, size int64) error {
	b.entries++
	if !isDir {
		b.bytes += size
	}
	if b.entries > ArchiveMaxEntries || b.bytes > ArchiveMaxTotalBytes {
		return ErrArchiveTooLarge
	}
	return nil
}

// addBytes charges post-walk growth against the byte budget only. The entry
// was already counted at walk time, so counting it again would double-count
// and could spuriously reject a legitimate archive near the entry limit.
func (b *archiveBudget) addBytes(size int64) error {
	b.bytes += size
	if b.bytes > ArchiveMaxTotalBytes {
		return ErrArchiveTooLarge
	}
	return nil
}

// isNonRegularFile reports modes that must never be opened for download or
// archiving: named pipes block in open(2) with no writer attached, and
// sockets/devices have no business in an archive. Symlinks are not flagged
// here — Stat follows them, so a link to a regular file still serves — but a
// link resolving to one of these modes is caught by re-statting the target.
func isNonRegularFile(mode os.FileMode) bool {
	return mode&os.ModeNamedPipe != 0 ||
		mode&os.ModeSocket != 0 ||
		mode&os.ModeDevice != 0 ||
		mode&os.ModeCharDevice != 0 ||
		mode&os.ModeIrregular != 0
}
