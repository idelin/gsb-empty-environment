package netsec

import "fmt"

func errNilScript() error {
	return fmt.Errorf("netsec: Solve requires a non-nil script")
}

func errBadBind(id string) error {
	return fmt.Errorf("netsec: bind operation %q requires exactly one source key", id)
}
