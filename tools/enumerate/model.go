package main

type Rect struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type AccessNode struct {
	ID                   string   `json:"id"`
	Role                 string   `json:"role"`
	Name                 *string  `json:"name"`
	ParentID             *string  `json:"parentId"`
	Rect                 *Rect    `json:"rect"`
	Hidden               bool     `json:"hidden"`
	IsLabel              bool     `json:"isLabel"`
	ExpectedControlRoles []string `json:"expectedControlRoles"`
}

type ExplicitRelation struct {
	LabelID   string `json:"labelId"`
	ControlID string `json:"controlId"`
	Kind      string `json:"kind"`
}

type Scenario struct {
	Name     string             `json:"name"`
	Nodes    []AccessNode       `json:"nodes"`
	Explicit []ExplicitRelation `json:"explicit"`
}

type Weights struct {
	Distance       float64 `json:"distance"`
	WrongDirection float64 `json:"wrongDirection"`
	RoleMismatch   float64 `json:"roleMismatch"`
}

type Options struct {
	Weights             *Weights `json:"weights"`
	PreferredDirections []string `json:"preferredDirections"`
	AmbiguityThreshold  *float64 `json:"ambiguityThreshold"`
	FormRoles           []string `json:"formRoles"`
	IncludeHidden       bool     `json:"includeHidden"`
}

type EdgeCost struct {
	LabelID            string   `json:"labelId"`
	ControlID          string   `json:"controlId"`
	RawDistance        float64  `json:"rawDistance"`
	Direction          string   `json:"direction"`
	PreferredDirection bool     `json:"preferredDirection"`
	RoleMismatch       bool     `json:"roleMismatch"`
	Cost               float64  `json:"cost"`
	Reasons            []string `json:"reasons"`
	Forbidden          bool     `json:"forbidden"`
}

type FieldBinding struct {
	LabelID        string     `json:"labelId"`
	LabelName      *string    `json:"labelName"`
	Status         string     `json:"status"`
	Source         string     `json:"source,omitempty"`
	ControlID      string     `json:"controlId,omitempty"`
	ControlRole    string     `json:"controlRole,omitempty"`
	Cost           float64    `json:"cost,omitempty"`
	SecondBestCost *float64   `json:"secondBestCost,omitempty"`
	Margin         *float64   `json:"margin,omitempty"`
	ExplicitKind   string     `json:"explicitKind,omitempty"`
	Edge           *EdgeCost  `json:"edge,omitempty"`
	Candidates     []EdgeCost `json:"candidates"`
	Reasons        []string   `json:"reasons"`
}

type BindingResult struct {
	Accepted       bool           `json:"accepted"`
	BestCost       *float64       `json:"bestCost"`
	SecondBestCost *float64       `json:"secondBestCost"`
	GlobalMargin   *float64       `json:"globalMargin"`
	Threshold      float64        `json:"threshold"`
	Fields         []FieldBinding `json:"fields"`
	Diagnostics    []string       `json:"diagnostics"`
}
