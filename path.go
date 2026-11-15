package netsec

import (
	"errors"
	"fmt"
)

// ReadState snapshots security.ima, security.selinux, security.evm, inode
// identity, and ownership/mode for evaluation before file access.
func ReadState(path string) (FileState, error) {
	return readState(path)
}

// EvaluatePath reads and evaluates a path. Every error is fail-closed.
func (e *Evaluator) EvaluatePath(path string) Result {
	if e == nil || (len(e.hostKey) == 0 && len(e.trustedKeys) == 0) {
		return denied(ReasonKeyUnavailable, 0, ErrKeyUnavailable.Error())
	}

	state, err := ReadState(path)
	if err != nil {
		switch {
		case errors.Is(err, ErrMissingEVM):
			return denied(ReasonMissingEVM, 0, err.Error())
		case errors.Is(err, ErrUnsupported):
			return denied(ReasonUnsupported, 0, err.Error())
		default:
			return denied(ReasonIOError, 0, fmt.Sprintf("read %q: %v", path, err))
		}
	}
	return e.Evaluate(state)
}
