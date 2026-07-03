package catalog

import "errors"

var (
	ErrNotFound = errors.New("catalog: not found")
	ErrConflict = errors.New("catalog: conflict")
	// ErrIllegalTransition is returned when a job status change is attempted from a
	// source state that does not allow it (e.g. completing an already-failed job).
	// The guarded transition methods return this instead of silently corrupting the
	// lifecycle — the single source of "job truth".
	ErrIllegalTransition = errors.New("catalog: illegal job status transition")
)
