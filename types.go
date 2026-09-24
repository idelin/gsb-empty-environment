package canonicalkey

type NullValue struct{}

type Number string

type Array []Value

type Object map[string]Value

type Ref struct {
	Node string
}

type Graph struct {
	Root  Ref
	Nodes map[string]Value
}

type Value interface{}

type StringField struct {
	Present bool
	Null    bool
	Value   string
}

func PresentString(value string) StringField {
	return StringField{Present: true, Value: value}
}

func NullString() StringField {
	return StringField{Present: true, Null: true}
}

type Params struct {
	Present bool
	Null    bool
	Value   Value
}

func AbsentParams() Params {
	return Params{}
}

func NullParams() Params {
	return Params{Present: true, Null: true}
}

func PresentParams(value Value) Params {
	return Params{Present: true, Value: value}
}

type Request struct {
	Tenant        string
	ParameterType string
	ConfigVersion StringField
	Params        Params
}
