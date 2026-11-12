package main

type edgeContext struct {
	index            *TreeIndex
	weights          Weights
	preferred        map[string]bool
	includeHidden    bool
	occupiedControls map[string]bool
}

func roleMatches(label, control *AccessNode) bool {
	if len(label.ExpectedControlRoles) == 0 {
		return true
	}
	for _, role := range label.ExpectedControlRoles {
		if role == control.Role {
			return true
		}
	}
	return false
}

func evaluateEdge(label, control *AccessNode, ctx edgeContext) EdgeCost {
	reasons := []string{}
	forbidden := false

	if !sharesFormAncestor(ctx.index, label.ID, control.ID) {
		forbidden = true
		reasons = append(reasons, "cross-form ancestor: label and control belong to different form/dialog subtrees")
	}
	if control.Hidden {
		if ctx.includeHidden {
			reasons = append(reasons, "control marked hidden (includeHidden=true)")
		} else {
			forbidden = true
			reasons = append(reasons, "control hidden: excluded from matching")
		}
	}
	if control.Rect == nil {
		forbidden = true
		reasons = append(reasons, "control has no visible rectangle")
	}
	if label.Rect == nil {
		forbidden = true
		reasons = append(reasons, "label has no visible rectangle")
	}
	if ctx.occupiedControls[control.ID] {
		forbidden = true
		reasons = append(reasons, "control already bound by an explicit relation")
	}

	var geometry *GeometryResult
	if label.Rect != nil && control.Rect != nil {
		g := relativeGeometry(*label.Rect, *control.Rect)
		geometry = &g
	}
	direction := "overlap"
	raw := 0.0
	preferred := false
	if geometry != nil {
		direction = geometry.Direction
		raw = geometry.RawDistance
		preferred = ctx.preferred[direction]
		if !preferred {
			reasons = append(reasons, "control is "+direction+" of label, preferred directions are not met")
		}
	}

	roleMismatch := !roleMatches(label, control)
	if roleMismatch {
		reasons = append(reasons, "role mismatch: control role '"+control.Role+"' not in expected list")
	}

	cost := ctx.weights.Distance * raw
	if !preferred {
		cost += ctx.weights.WrongDirection
	}
	if roleMismatch {
		cost += ctx.weights.RoleMismatch
	}
	if forbidden {
		cost = forbiddenCost
	}

	return EdgeCost{
		LabelID:            label.ID,
		ControlID:          control.ID,
		RawDistance:        raw,
		Direction:          direction,
		PreferredDirection: preferred,
		RoleMismatch:       roleMismatch,
		Cost:               cost,
		Reasons:            reasons,
		Forbidden:          forbidden,
	}
}
