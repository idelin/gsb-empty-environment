package objectgraph

import "fmt"

func wrapf(err error, format string, a ...any) error {
	return fmt.Errorf("%w: %s", err, fmt.Sprintf(format, a...))
}
