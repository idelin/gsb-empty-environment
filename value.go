package main

import (
	"encoding/json"
	"fmt"
)

type Kind int

const (
	Null Kind = iota
	Int
	Str
)

type Value struct {
	Kind Kind
	I    int64
	S    string
}

func NullV() Value        { return Value{Kind: Null} }
func IntV(i int64) Value  { return Value{Kind: Int, I: i} }
func StrV(s string) Value { return Value{Kind: Str, S: s} }

func (v Value) String() string {
	switch v.Kind {
	case Null:
		return "NULL"
	case Int:
		return fmt.Sprintf("%d", v.I)
	case Str:
		return fmt.Sprintf("%q", v.S)
	}
	return "?"
}

func (v Value) key() string {
	switch v.Kind {
	case Null:
		return "N"
	case Int:
		return fmt.Sprintf("I:%d", v.I)
	case Str:
		return "S:" + v.S
	}
	return "?"
}

func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case Null:
		return []byte("null"), nil
	case Int:
		return json.Marshal(v.I)
	case Str:
		return json.Marshal(v.S)
	}
	return nil, fmt.Errorf("bad value kind %d", v.Kind)
}

type Tri int8

const (
	False Tri = iota
	Unknown
	True
)

func (t Tri) String() string {
	switch t {
	case False:
		return "false"
	case Unknown:
		return "unknown"
	case True:
		return "true"
	}
	return "?"
}

func tri(b bool) Tri {
	if b {
		return True
	}
	return False
}

func And(a, b Tri) Tri {
	if a < b {
		return a
	}
	return b
}

func Or(a, b Tri) Tri {
	if a > b {
		return a
	}
	return b
}

func Not(a Tri) Tri {
	switch a {
	case True:
		return False
	case False:
		return True
	}
	return Unknown
}
