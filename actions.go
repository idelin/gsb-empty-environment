package iac

import "strings"

// ActionKind is the lifecycle action selected for a resource.
//
// ActionReplace is a planning-time action. After create_before_destroy
// propagation it is always lowered to either ActionCreateThenDestroy or
// ActionDestroyThenCreate.
type ActionKind string

const (
	ActionNoOp              ActionKind = "NoOp"
	ActionCreate            ActionKind = "Create"
	ActionUpdateInPlace     ActionKind = "UpdateInPlace"
	ActionDestroy           ActionKind = "Destroy"
	ActionCreateThenDestroy ActionKind = "CreateThenDestroy"
	ActionDestroyThenCreate ActionKind = "DestroyThenCreate"
	ActionReplace           ActionKind = "Replace"
)

// Short aliases make plans and tests easier to read.
const (
	NoOp              = ActionNoOp
	Create            = ActionCreate
	UpdateInPlace     = ActionUpdateInPlace
	Destroy           = ActionDestroy
	CreateThenDestroy = ActionCreateThenDestroy
	DestroyThenCreate = ActionDestroyThenCreate
	Replace           = ActionReplace
)

// IsReplace reports whether the action replaces an existing object.
func (a ActionKind) IsReplace() bool {
	switch a {
	case ActionReplace, ActionCreateThenDestroy, ActionDestroyThenCreate:
		return true
	default:
		return false
	}
}

// DestroysExisting reports whether the action destroys an existing object.
// Both replacement forms contain a destroy operation.
func (a ActionKind) DestroysExisting() bool {
	switch a {
	case ActionDestroy, ActionCreateThenDestroy, ActionDestroyThenCreate:
		return true
	default:
		return false
	}
}

// Creates reports whether the action has a create operation.
func (a ActionKind) Creates() bool {
	switch a {
	case ActionCreate, ActionCreateThenDestroy, ActionDestroyThenCreate:
		return true
	default:
		return false
	}
}

// Valid reports whether a is a known action.
func (a ActionKind) Valid() bool {
	switch a {
	case ActionNoOp, ActionCreate, ActionUpdateInPlace, ActionDestroy,
		ActionCreateThenDestroy, ActionDestroyThenCreate, ActionReplace:
		return true
	default:
		return false
	}
}

// ParseAction accepts the canonical action names and a few common spellings.
func ParseAction(s string) (ActionKind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "noop", "no-op", "no_op", "none":
		return ActionNoOp, true
	case "create", "add":
		return ActionCreate, true
	case "update", "updateinplace", "update-in-place", "update_in_place":
		return ActionUpdateInPlace, true
	case "destroy", "delete", "remove":
		return ActionDestroy, true
	case "replace":
		return ActionReplace, true
	case "createthendestroy", "create-then-destroy", "create_then_destroy",
		"createbeforedestroy", "create-before-destroy", "create_before_destroy", "cbd":
		return ActionCreateThenDestroy, true
	case "destroythencreate", "destroy-then-create", "destroy_then_create",
		"destroybeforecreate", "destroy-before-create", "destroy_before_create":
		return ActionDestroyThenCreate, true
	default:
		return "", false
	}
}
