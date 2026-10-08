package material

import "fmt"

// Lot is a material entity: an integer quantity carrying a provenance vector.
type Lot struct {
	ID  string
	Qty int64
	Vec Vector // shares sum to exactly 1
}

func (l *Lot) Clone() *Lot {
	return &Lot{ID: l.ID, Qty: l.Qty, Vec: l.Vec.Clone()}
}

func (l *Lot) Equal(o *Lot) bool {
	return l.ID == o.ID && l.Qty == o.Qty && l.Vec.Equal(o.Vec)
}

func (l *Lot) String() string {
	return fmt.Sprintf("Lot{%s qty=%d vec=[%s]}", l.ID, l.Qty, l.Vec)
}
