package main

import "fmt"

// Kind is the operation type of a single-key register operation.
type Kind int

const (
	Read Kind = iota
	Write
)

func (k Kind) String() string {
	if k == Read {
		return "R"
	}
	return "W"
}

// Op is one invocation/response interval of a register operation.
// Respond == -1 means the call never completed (incomplete/pending).
type Op struct {
	ID      int
	Client  int
	Kind    Kind
	Value   int // argument for Write, returned value for Read
	Invoke  int64
	Respond int64 // -1 if incomplete
}

func (o Op) Complete() bool { return o.Respond >= 0 }

func (o Op) String() string {
	if o.Complete() {
		return fmt.Sprintf("#%d c%d %s(%d) [%d,%d]", o.ID, o.Client, o.Kind, o.Value, o.Invoke, o.Respond)
	}
	return fmt.Sprintf("#%d c%d %s(%d) [%d,_]", o.ID, o.Client, o.Kind, o.Value, o.Invoke)
}

// History is a set of operations on one register.
type History struct {
	Ops []Op
}

func (h *History) String() string {
	s := ""
	for _, o := range h.Ops {
		s += "  " + o.String() + "\n"
	}
	return s
}
