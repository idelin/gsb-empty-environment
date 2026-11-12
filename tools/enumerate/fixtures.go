package main

func rect(x, y float64) *Rect {
	return &Rect{X: x, Y: y, Width: 80, Height: 24}
}

func rectSized(x, y, width, height float64) *Rect {
	return &Rect{X: x, Y: y, Width: width, Height: height}
}

func str(value string) *string { return &value }

func crossFormScenario() Scenario {
	return Scenario{
		Name: "cross-form",
		Nodes: []AccessNode{
			{ID: "form-a", Role: "form", Name: str("A"), Rect: rectSized(0, 0, 400, 200)},
			{ID: "form-b", Role: "form", Name: str("B"), Rect: rectSized(0, 240, 400, 200)},
			{ID: "a-label-1", Role: "label", IsLabel: true, Name: str("A1"), ParentID: str("form-a"), Rect: rect(10, 20), ExpectedControlRoles: []string{"textbox"}},
			{ID: "a-label-2", Role: "label", IsLabel: true, Name: str("A2"), ParentID: str("form-a"), Rect: rect(10, 70), ExpectedControlRoles: []string{"textbox"}},
			{ID: "a-input-1", Role: "textbox", ParentID: str("form-a"), Rect: rect(120, 20)},
			{ID: "a-input-2", Role: "textbox", ParentID: str("form-a"), Rect: rect(120, 70)},
			{ID: "b-label-1", Role: "label", IsLabel: true, Name: str("B1"), ParentID: str("form-b"), Rect: rect(10, 260), ExpectedControlRoles: []string{"textbox"}},
			{ID: "b-label-2", Role: "label", IsLabel: true, Name: str("B2"), ParentID: str("form-b"), Rect: rect(10, 310), ExpectedControlRoles: []string{"textbox"}},
			{ID: "b-input-1", Role: "textbox", ParentID: str("form-b"), Rect: rect(120, 260)},
			{ID: "b-input-2", Role: "textbox", ParentID: str("form-b"), Rect: rect(120, 310)},
		},
	}
}

func duplicateLabelScenario() Scenario {
	return Scenario{
		Name: "duplicate-labels",
		Nodes: []AccessNode{
			{ID: "dlg", Role: "dialog", Rect: rectSized(0, 0, 400, 200)},
			{ID: "dup-label-1", Role: "label", IsLabel: true, Name: str("Name"), ParentID: str("dlg"), Rect: rect(10, 20)},
			{ID: "dup-label-2", Role: "label", IsLabel: true, Name: str("Name"), ParentID: str("dlg"), Rect: rect(10, 60)},
			{ID: "dup-input-1", Role: "textbox", ParentID: str("dlg"), Rect: rect(120, 20)},
			{ID: "dup-input-2", Role: "textbox", ParentID: str("dlg"), Rect: rect(124, 60)},
		},
	}
}

func hiddenControlScenario() Scenario {
	return Scenario{
		Name: "hidden-control",
		Nodes: []AccessNode{
			{ID: "dlg", Role: "dialog", Rect: rectSized(0, 0, 400, 200)},
			{ID: "vis-label", Role: "label", IsLabel: true, Name: str("Visible"), ParentID: str("dlg"), Rect: rect(10, 20)},
			{ID: "hid-label", Role: "label", IsLabel: true, Name: str("Hidden"), ParentID: str("dlg"), Rect: rect(10, 70)},
			{ID: "vis-input", Role: "textbox", ParentID: str("dlg"), Rect: rect(120, 20)},
			{ID: "hid-input", Role: "textbox", ParentID: str("dlg"), Hidden: true, Rect: rect(120, 70)},
		},
	}
}

func nestedDialogScenario() Scenario {
	return Scenario{
		Name: "nested-dialog",
		Nodes: []AccessNode{
			{ID: "outer-form", Role: "form", Rect: rectSized(0, 0, 500, 400)},
			{ID: "outer-label", Role: "label", IsLabel: true, Name: str("Outer"), ParentID: str("outer-form"), Rect: rect(10, 20)},
			{ID: "outer-input", Role: "textbox", ParentID: str("outer-form"), Rect: rect(120, 20)},
			{ID: "modal", Role: "dialog", ParentID: str("outer-form"), Rect: rectSized(20, 80, 460, 300)},
			{ID: "modal-label", Role: "label", IsLabel: true, Name: str("Modal"), ParentID: str("modal"), Rect: rect(30, 100)},
			{ID: "modal-input", Role: "textbox", ParentID: str("modal"), Rect: rect(150, 100)},
		},
	}
}

func explicitPriorityScenario() Scenario {
	return Scenario{
		Name: "explicit-priority",
		Nodes: []AccessNode{
			{ID: "dlg", Role: "dialog", Rect: rectSized(0, 0, 400, 200)},
			{ID: "exp-label", Role: "label", IsLabel: true, Name: str("Token"), ParentID: str("dlg"), Rect: rect(10, 20)},
			{ID: "near-input", Role: "textbox", ParentID: str("dlg"), Rect: rect(120, 20)},
			{ID: "far-input", Role: "textbox", ParentID: str("dlg"), Rect: rect(300, 120)},
		},
		Explicit: []ExplicitRelation{{LabelID: "exp-label", ControlID: "far-input", Kind: "for"}},
	}
}

func roleMismatchScenario() Scenario {
	return Scenario{
		Name: "role-mismatch",
		Nodes: []AccessNode{
			{ID: "dlg", Role: "dialog", Rect: rectSized(0, 0, 400, 200)},
			{ID: "lbl", Role: "label", IsLabel: true, Name: str("Agree"), ParentID: str("dlg"), Rect: rectSized(10, 20, 60, 24), ExpectedControlRoles: []string{"checkbox"}},
			{ID: "close-text", Role: "textbox", ParentID: str("dlg"), Rect: rectSized(90, 20, 80, 24)},
			{ID: "far-check", Role: "checkbox", ParentID: str("dlg"), Rect: rectSized(130, 20, 20, 20)},
		},
	}
}

func allScenarios() []Scenario {
	return []Scenario{
		crossFormScenario(),
		duplicateLabelScenario(),
		nestedDialogScenario(),
		hiddenControlScenario(),
		explicitPriorityScenario(),
		roleMismatchScenario(),
	}
}
