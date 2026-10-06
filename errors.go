package stats

import "fmt"

// UnderflowError reports a removal of more weight than present.
type UnderflowError struct {
	Value      int64
	Have, Want uint64
}

func (e *UnderflowError) Error() string {
	return fmt.Sprintf("stats: remove underflow for value %d: have %d, want %d", e.Value, e.Have, e.Want)
}
