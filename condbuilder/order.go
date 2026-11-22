package condbuilder

import (
	"fmt"
	"strings"
)

// Direction is the fixed set of allowed ORDER BY directions.
type Direction string

const (
	Asc  Direction = "ASC"
	Desc Direction = "DESC"
)

// Order is one ORDER BY key. Both the field and the direction are validated
// against fixed sets; no external text reaches the SQL string.
type Order struct {
	Field string
	Dir   Direction
}

// By is shorthand for an ascending order key.
func By(field string) Order { return Order{Field: field, Dir: Asc} }

// ByDesc is shorthand for a descending order key.
func ByDesc(field string) Order { return Order{Field: field, Dir: Desc} }

func (o Order) render(w *Whitelist) (string, error) {
	if o.Dir != Asc && o.Dir != Desc {
		return "", fmt.Errorf("order field %q: invalid direction %q (only ASC/DESC)", o.Field, o.Dir)
	}
	ident, err := w.render(o.Field)
	if err != nil {
		return "", err
	}
	return ident + " " + string(o.Dir), nil
}

func renderOrders(w *Whitelist, orders []Order) (string, error) {
	if len(orders) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(orders))
	for _, o := range orders {
		rendered, err := o.render(w)
		if err != nil {
			return "", err
		}
		parts = append(parts, rendered)
	}
	return "ORDER BY " + strings.Join(parts, ", "), nil
}
