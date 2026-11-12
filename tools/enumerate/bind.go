package main

import (
	"math"
	"sort"
)

var defaultWeights = Weights{Distance: 1, WrongDirection: 100, RoleMismatch: 50}

var defaultPreferred = map[string]bool{"right": true, "below": true}

var defaultFormRoles = map[string]bool{"form": true, "dialog": true}

const defaultThreshold = 1.0

func resolveOptions(opts *Options) (Weights, map[string]bool, float64, map[string]bool, bool) {
	weights := defaultWeights
	if opts != nil && opts.Weights != nil {
		weights = *opts.Weights
	}
	preferred := defaultPreferred
	if opts != nil && len(opts.PreferredDirections) > 0 {
		preferred = map[string]bool{}
		for _, direction := range opts.PreferredDirections {
			preferred[direction] = true
		}
	}
	threshold := defaultThreshold
	if opts != nil && opts.AmbiguityThreshold != nil {
		threshold = *opts.AmbiguityThreshold
	}
	formRoles := defaultFormRoles
	if opts != nil && len(opts.FormRoles) > 0 {
		formRoles = map[string]bool{}
		for _, role := range opts.FormRoles {
			formRoles[role] = true
		}
	}
	includeHidden := opts != nil && opts.IncludeHidden
	return weights, preferred, threshold, formRoles, includeHidden
}

func assignmentWithoutEdge(costs [][]float64, blockedRow, blockedCol int) float64 {
	copyMatrix := make([][]float64, len(costs))
	for row := range costs {
		copyMatrix[row] = append([]float64(nil), costs[row]...)
	}
	copyMatrix[blockedRow][blockedCol] = forbiddenCost
	alternative := minCostAssignmentAnyShape(copyMatrix)
	if !alternative.Feasible {
		return math.Inf(1)
	}
	return alternative.Cost
}

func bindForms(nodes []AccessNode, explicit []ExplicitRelation, opts *Options) (*BindingResult, error) {
	weights, preferred, threshold, formRoleSet, includeHidden := resolveOptions(opts)
	index, err := indexTree(nodes, formRoleSet)
	if err != nil {
		return nil, err
	}

	diagnostics := []string{}
	fields := []FieldBinding{}
	var labels []*AccessNode
	var controls []*AccessNode
	for i := range nodes {
		n := &nodes[i]
		if n.IsLabel {
			labels = append(labels, n)
		} else if !formRoleSet[n.Role] {
			controls = append(controls, n)
		}
	}

	explicitLabelIDs := map[string]bool{}
	occupied := map[string]bool{}

	// Phase 1: explicit relations.
	for _, relation := range explicit {
		label := index.Nodes[relation.LabelID]
		control := index.Nodes[relation.ControlID]
		field := FieldBinding{
			LabelID:    relation.LabelID,
			Candidates: []EdgeCost{},
			Reasons:    []string{},
			Status:     "unbound",
		}
		if label != nil {
			field.LabelName = label.Name
		}
		if label == nil || control == nil {
			field.Reasons = append(field.Reasons, "explicit relation references unknown node")
			diagnostics = append(diagnostics, field.Reasons[len(field.Reasons)-1])
			fields = append(fields, field)
			continue
		}
		if occupied[control.ID] {
			field.Reasons = append(field.Reasons, "explicit control already claimed")
			diagnostics = append(diagnostics, field.Reasons[len(field.Reasons)-1])
			fields = append(fields, field)
			continue
		}
		edge := evaluateEdge(label, control, edgeContext{index, weights, preferred, includeHidden, occupied})
		field.Candidates = append(field.Candidates, edge)
		field.Edge = &edge
		if edge.Forbidden {
			field.Reasons = append(field.Reasons, edge.Reasons...)
			field.Reasons = append(field.Reasons, "explicit relation rejected; label falls back to implicit matching")
			diagnostics = append(diagnostics, "explicit relation "+relation.LabelID+" -> "+relation.ControlID+" rejected")
			fields = append(fields, field)
			continue
		}
		explicitLabelIDs[label.ID] = true
		occupied[control.ID] = true
		field.Status = "bound"
		field.Source = "explicit"
		field.ControlID = control.ID
		field.ControlRole = control.Role
		field.Cost = edge.Cost
		field.ExplicitKind = relation.Kind
		formName := index.FormRoot[control.ID]
		formLabel := "(no form)"
		if formName != nil {
			formLabel = *formName
		}
		field.Reasons = append(field.Reasons, "explicit relation binds label within form "+formLabel)
		fields = append(fields, field)
	}

	// Phase 2: implicit global min-cost matching.
	var implicitLabels []*AccessNode
	for _, label := range labels {
		if !explicitLabelIDs[label.ID] {
			implicitLabels = append(implicitLabels, label)
		}
	}
	var availableControls []*AccessNode
	for _, control := range controls {
		if !occupied[control.ID] {
			availableControls = append(availableControls, control)
		}
	}

	edges := make([][]EdgeCost, len(implicitLabels))
	costs := make([][]float64, len(implicitLabels))
	for row, label := range implicitLabels {
		edges[row] = make([]EdgeCost, len(availableControls))
		costs[row] = make([]float64, len(availableControls))
		for col, control := range availableControls {
			edge := evaluateEdge(label, control, edgeContext{index, weights, preferred, includeHidden, occupied})
			edges[row][col] = edge
			costs[row][col] = edge.Cost
		}
	}

	noControlsAvailable := len(availableControls) == 0 && len(implicitLabels) > 0
	var assignment Assignment
	if noControlsAvailable {
		rowToCol := make([]int, len(implicitLabels))
		for i := range rowToCol {
			rowToCol[i] = -1
		}
		assignment = Assignment{RowToCol: rowToCol, Cost: math.Inf(1), Feasible: false}
	} else {
		assignment = minCostAssignmentAnyShape(costs)
	}
	feasible := assignment.Feasible && !noControlsAvailable
	if feasible {
		for _, col := range assignment.RowToCol {
			if col < 0 {
				feasible = false
			}
		}
	}

	secondBest := math.Inf(1)
	if feasible {
		for row := range implicitLabels {
			col := assignment.RowToCol[row]
			alternative := assignmentWithoutEdge(costs, row, col)
			if alternative < secondBest && alternative > assignment.Cost+1e-9 {
				secondBest = alternative
			}
		}
	}
	var globalMargin *float64
	if feasible && !math.IsInf(secondBest, 1) {
		value := secondBest - assignment.Cost
		globalMargin = &value
	}

	for row, label := range implicitLabels {
		sorted := append([]EdgeCost(nil), edges[row]...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Cost < sorted[j].Cost })
		field := FieldBinding{
			LabelID:    label.ID,
			LabelName:  label.Name,
			Status:     "unbound",
			Source:     "implicit",
			Candidates: sorted,
			Reasons:    []string{},
		}
		col := -1
		if feasible {
			col = assignment.RowToCol[row]
		}
		if col < 0 {
			field.Reasons = append(field.Reasons, "no feasible one-to-one assignment: every allowed edge is forbidden")
			diagnostics = append(diagnostics, "label "+label.ID+" could not be matched")
			fields = append(fields, field)
			continue
		}
		chosen := edges[row][col]
		alternative := assignmentWithoutEdge(costs, row, col)
		var margin *float64
		if !math.IsInf(alternative, 1) {
			value := alternative - chosen.Cost
			margin = &value
			field.SecondBestCost = &alternative
			field.Margin = margin
		}
		edgeCopy := chosen
		field.Edge = &edgeCopy
		field.ControlID = chosen.ControlID
		field.ControlRole = availableControls[col].Role
		field.Cost = chosen.Cost
		field.Reasons = append(field.Reasons, "implicit min-cost match: "+chosen.Direction+" weighted cost")
		field.Reasons = append(field.Reasons, chosen.Reasons...)
		if margin != nil && *margin < threshold {
			field.Status = "ambiguous"
			field.Reasons = append(field.Reasons, "ambiguous: margin below threshold")
		} else {
			field.Status = "bound"
		}
		fields = append(fields, field)
	}

	labelOrder := map[string]int{}
	for i, label := range labels {
		labelOrder[label.ID] = i
	}
	sort.SliceStable(fields, func(i, j int) bool {
		return labelOrder[fields[i].LabelID] < labelOrder[fields[j].LabelID]
	})

	ambiguous := false
	for _, field := range fields {
		if field.Status != "bound" {
			ambiguous = true
		}
	}
	globalAmbiguous := globalMargin != nil && *globalMargin < threshold
	accepted := feasible && !ambiguous && !globalAmbiguous
	if globalAmbiguous {
		diagnostics = append(diagnostics, "global assignment margin below threshold")
	}

	result := &BindingResult{
		Accepted:     accepted,
		GlobalMargin: globalMargin,
		Threshold:    threshold,
		Fields:       fields,
		Diagnostics:  diagnostics,
	}
	if feasible {
		best := assignment.Cost
		result.BestCost = &best
	}
	if !math.IsInf(secondBest, 1) {
		value := secondBest
		result.SecondBestCost = &value
	}
	return result, nil
}
