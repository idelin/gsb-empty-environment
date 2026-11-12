package main

import (
	"fmt"
	"math"
	"os"
	"strings"
)

var failures int

func check(condition bool, message string) {
	if !condition {
		failures++
		fmt.Printf("  FAIL: %s\n", message)
	} else {
		fmt.Printf("  ok:   %s\n", message)
	}
}

func implicitMatrix(scenario Scenario) [][]float64 {
	index, err := indexTree(scenario.Nodes, defaultFormRoles)
	if err != nil {
		panic(err)
	}
	occupied := map[string]bool{}
	explicitLabels := map[string]bool{}
	for _, relation := range scenario.Explicit {
		control := index.Nodes[relation.ControlID]
		if control != nil && !control.Hidden && control.Rect != nil {
			occupied[control.ID] = true
			explicitLabels[relation.LabelID] = true
		}
	}
	var labelIDs, controlIDs []string
	for _, node := range scenario.Nodes {
		if node.IsLabel && !explicitLabels[node.ID] {
			labelIDs = append(labelIDs, node.ID)
		}
	}
	for _, node := range scenario.Nodes {
		if !node.IsLabel && !occupied[node.ID] {
			controlIDs = append(controlIDs, node.ID)
		}
	}
	costs := make([][]float64, len(labelIDs))
	for row, labelID := range labelIDs {
		costs[row] = make([]float64, len(controlIDs))
		for col, controlID := range controlIDs {
			edge := evaluateEdge(
				index.Nodes[labelID],
				index.Nodes[controlID],
				edgeContext{index, defaultWeights, defaultPreferred, false, occupied},
			)
			costs[row][col] = edge.Cost
		}
	}
	return costs
}

func fieldByID(result *BindingResult, id string) FieldBinding {
	for _, field := range result.Fields {
		if field.LabelID == id {
			return field
		}
	}
	panic("missing field " + id)
}

func rerunSecondBest(costs [][]float64, bestCost float64, rowToCol []int) float64 {
	second := math.Inf(1)
	for row := range costs {
		alt := assignmentWithoutEdge(costs, row, rowToCol[row])
		if alt < second && alt > bestCost+1e-9 {
			second = alt
		}
	}
	return second
}

func main() {
	fmt.Println("== Hungarian optimum vs full enumeration ==")
	for _, scenario := range allScenarios() {
		costs := implicitMatrix(scenario)
		if len(costs) == 0 {
			fmt.Printf("- %s: no implicit labels, skipped\n", scenario.Name)
			continue
		}
		best, second := bestAndSecond(costs)
		assignment := minCostAssignmentAnyShape(costs)
		check(best != nil, scenario.Name+": enumeration has a best assignment")
		check(assignment.Feasible, scenario.Name+": Hungarian assignment feasible")
		check(math.Abs(assignment.Cost-best.Cost) < 1e-9,
			fmt.Sprintf("%s: Hungarian %.4f == enumeration %.4f", scenario.Name, assignment.Cost, best.Cost))
		rerun := rerunSecondBest(costs, assignment.Cost, assignment.RowToCol)
		if second != nil {
			check(math.Abs(rerun-second.Cost) < 1e-9,
				fmt.Sprintf("%s: rerun second-best %.4f == enumeration %.4f", scenario.Name, rerun, second.Cost))
		}
	}

	fmt.Println("\n== cross-form ancestors ==")
	zero := 0.0
	result, err := bindForms(crossFormScenario().Nodes, nil, &Options{AmbiguityThreshold: &zero})
	if err != nil {
		panic(err)
	}
	check(result.Accepted, "cross-form result accepted")
	check(fieldByID(result, "a-label-1").ControlID == "a-input-1", "a-label-1 -> a-input-1")
	check(fieldByID(result, "a-label-2").ControlID == "a-input-2", "a-label-2 -> a-input-2")
	check(fieldByID(result, "b-label-1").ControlID == "b-input-1", "b-label-1 -> b-input-1")
	check(fieldByID(result, "b-label-2").ControlID == "b-input-2", "b-label-2 -> b-input-2")
	for _, field := range result.Fields {
		check(field.Edge != nil && !field.Edge.Forbidden, field.LabelID+" chosen edge allowed")
		hasForbidden := false
		for _, edge := range field.Candidates {
			if edge.Forbidden {
				hasForbidden = true
			}
		}
		check(hasForbidden, field.LabelID+" records forbidden cross-form candidates")
	}

	fmt.Println("\n== duplicate labels / ambiguity gate ==")
	loose, _ := bindForms(duplicateLabelScenario().Nodes, nil, &Options{AmbiguityThreshold: &zero})
	check(loose.Accepted, "duplicate labels accepted with threshold 0")
	ten := 10.0
	strict, _ := bindForms(duplicateLabelScenario().Nodes, nil, &Options{AmbiguityThreshold: &ten})
	check(!strict.Accepted, "duplicate labels rejected with threshold 10")
	check(strict.GlobalMargin != nil && *strict.GlobalMargin < 10, "global margin below threshold drives rejection")
	check(len(strict.Diagnostics) > 0, "rejection recorded in diagnostics")

	fmt.Println("\n== hidden controls ==")
	hiddenResult, _ := bindForms(hiddenControlScenario().Nodes, nil, nil)
	hiddenField := fieldByID(hiddenResult, "hid-label")
	check(hiddenField.Status == "unbound", "hidden-control label unbound by default")
	joined := ""
	hiddenEdgeForbidden := false
	for _, edge := range hiddenField.Candidates {
		if edge.ControlID == "hid-input" && edge.Forbidden {
			hiddenEdgeForbidden = true
		}
		joined += strings.Join(edge.Reasons, " ") + " "
	}
	check(hiddenEdgeForbidden, "edge to the hidden control is forbidden")
	check(strings.Contains(joined, "hidden"), "hidden reason present in evidence")
	included, _ := bindForms(hiddenControlScenario().Nodes, nil, &Options{IncludeHidden: true})
	check(fieldByID(included, "hid-label").ControlID == "hid-input", "hidden control bindable with includeHidden")

	fmt.Println("\n== nested dialog ==")
	nested, _ := bindForms(nestedDialogScenario().Nodes, nil, &Options{AmbiguityThreshold: &zero})
	check(nested.Accepted, "nested dialog accepted")
	check(fieldByID(nested, "outer-label").ControlID == "outer-input", "outer label stays in outer form")
	modal := fieldByID(nested, "modal-label")
	check(modal.ControlID == "modal-input", "modal label binds modal control")
	modalForbidden := false
	for _, edge := range modal.Candidates {
		if edge.ControlID == "outer-input" && edge.Forbidden {
			modalForbidden = true
		}
	}
	check(modalForbidden, "modal -> outer-input edge forbidden")

	fmt.Println("\n== explicit priority ==")
	scenario := explicitPriorityScenario()
	explicit, _ := bindForms(scenario.Nodes, scenario.Explicit, nil)
	expField := fieldByID(explicit, "exp-label")
	check(explicit.Accepted, "explicit-priority result accepted")
	check(expField.Source == "explicit" && expField.ControlID == "far-input", "explicit relation wins over near input")
	check(expField.ExplicitKind == "for", "relation kind recorded")

	fmt.Println("\n== role mismatch and caller weights ==")
	roleResult, _ := bindForms(roleMismatchScenario().Nodes, nil, &Options{AmbiguityThreshold: &zero})
	roleField := fieldByID(roleResult, "lbl")
	check(roleField.ControlID == "far-check", "role penalty selects the checkbox despite distance")
	var textboxEdge EdgeCost
	for _, edge := range roleField.Candidates {
		if edge.ControlID == "close-text" {
			textboxEdge = edge
		}
	}
	check(textboxEdge.RoleMismatch, "textbox edge carries role mismatch flag")
	zeroPenalty, _ := bindForms(roleMismatchScenario().Nodes, nil, &Options{
		AmbiguityThreshold: &zero,
		Weights:            &Weights{Distance: 1, WrongDirection: 100, RoleMismatch: 0},
	})
	check(fieldByID(zeroPenalty, "lbl").ControlID == "close-text", "zero role penalty flips selection to near textbox")

	fmt.Println("\n== evidence completeness ==")
	for _, field := range result.Fields {
		check(len(field.Reasons) > 0, field.LabelID+" has audit reasons")
		check(len(field.Candidates) > 0, field.LabelID+" has candidate evidence")
		sorted := true
		for i := 1; i < len(field.Candidates); i++ {
			if field.Candidates[i].Cost < field.Candidates[i-1].Cost {
				sorted = false
			}
		}
		check(sorted, field.LabelID+" candidates sorted by cost")
	}

	fmt.Printf("\n%d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
