package temporal

import (
	"fmt"
	"strconv"
)

func itoa(i int) string { return strconv.Itoa(i) }

func formatAny(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}
