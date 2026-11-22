package mtuinfer

import (
	"fmt"
	"sort"
)

// VerifyResult independently replays the receipts and proofs against the
// original input. It returns ErrInvalidInput when a proof cannot be checked.
func VerifyResult(in Input, result Result) error {
	prep, err := prepare(in)
	if err != nil {
		return err
	}
	if !result.Compatible {
		return invalid("incompatible result requires ErrInconsistentEvidence and no proofs")
	}
	if err := verifyUniversal(prep, result.Guaranteed); err != nil {
		return err
	}
	if err := verifyPossibility(prep, result.Possible); err != nil {
		return err
	}
	return nil
}

func verifyUniversal(prep *prepared, result PayloadResult) error {
	proof := result.UniversalProof
	if proof == nil {
		return invalid("missing universal proof")
	}
	if proof.GuaranteedPayload != result.Payload {
		return invalid("universal proof payload does not match result")
	}
	if len(proof.Assignments) == 0 {
		return invalid("universal proof has no assignments")
	}
	seenAssignments := make(map[string]struct{})
	var minimum int = int(^uint(0) >> 1)

	for _, cert := range proof.Assignments {
		routes, _, err := resolveChoices(prep, cert.PathChoices)
		if err != nil {
			return err
		}
		assignmentKey := assignmentKey(cert.PathChoices)
		if _, exists := seenAssignments[assignmentKey]; exists {
			return invalid("duplicate assignment in universal proof")
		}
		seenAssignments[assignmentKey] = struct{}{}

		lower, failureReasons, ok := replayAssignment(prep, routes)
		if !ok {
			return ErrInconsistentEvidence
		}
		if !equalBoundMaps(lower, linkBoundsToMap(cert.GuaranteedMTUs)) {
			return invalid("certificate MTU lower envelope is incorrect")
		}
		if err := verifyFailureReasons(prep, routes, failureReasons, cert.FailureReasons); err != nil {
			return err
		}
		checks := selectedTargetRoutePayloads(prep, routes, lower)
		certMinimum := minimumRoutePayload(checks)
		if certMinimum != cert.GuaranteedPayload {
			return invalid("certificate guaranteed payload is incorrect")
		}
		if cert.TargetRouteID != "" && routePayloadValue(checks, cert.TargetRouteID) != certMinimum {
			return invalid("certificate target route is not a minimum witness")
		}
		if certMinimum < minimum {
			minimum = certMinimum
		}
	}

	if err := verifyAllAssignmentCoverage(prep, seenAssignments); err != nil {
		return err
	}
	if minimum != proof.GuaranteedPayload {
		return invalid("universal payload does not equal target lower envelope")
	}
	return nil
}

func selectedTargetRoutePayloads(prep *prepared, routes map[PathKey]Route, mtu map[string]int) []RoutePayload {
	targetRoutes := []Route{routes[prep.targetKey]}
	if prep.in.Target.RouteID != "" {
		targetRoutes = targetRoutePayloadsRoutes(prep)
	}
	checks := make([]RoutePayload, 0, len(targetRoutes))
	for _, route := range targetRoutes {
		payload, ok := maxPayloadForRoute(route, mtu, prep.in.Context)
		if !ok {
			payload = -1
		}
		checks = append(checks, RoutePayload{RouteID: route.ID, Payload: payload})
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].RouteID < checks[j].RouteID })
	return checks
}

func targetRoutePayloadsRoutes(prep *prepared) []Route {
	for _, route := range prep.routeSets[prep.targetKey] {
		if route.ID == prep.in.Target.RouteID {
			return []Route{route}
		}
	}
	return nil
}

func verifyPossibility(prep *prepared, result PayloadResult) error {
	proof := result.PossibilityProof
	if proof == nil {
		return invalid("missing possibility proof")
	}
	if proof.MaximumPayload != result.Payload {
		return invalid("possibility proof payload does not match result")
	}
	if proof.Witness == nil || result.Witness == nil {
		return invalid("missing possibility witness")
	}
	if !equalWitness(proof.Witness, result.Witness) {
		return invalid("possibility witness appears twice with different values")
	}
	if proof.MaximumPayload >= 0 {
		if err := verifyWitness(prep, proof.Witness, proof.MaximumPayload); err != nil {
			return err
		}
	}

	seen := make(map[string]struct{})
	maximum := -1
	for _, item := range proof.Assignments {
		routes, _, err := resolveChoices(prep, item.Certificate.PathChoices)
		if err != nil {
			return err
		}
		key := assignmentKey(item.Certificate.PathChoices)
		if _, exists := seen[key]; exists {
			return invalid("duplicate assignment in possibility proof")
		}
		seen[key] = struct{}{}
		lower, _, feasible := replayAssignment(prep, routes)
		if !feasible {
			return ErrInconsistentEvidence
		}
		expected := analyzePossibility(prep, assignment{chosen: routes}, fixedResult{
			cert:       item.Certificate,
			lower:      lower,
			maxPayload: item.Certificate.GuaranteedPayload,
		})
		if expected.MaximumPayload != item.MaximumPayload {
			return invalid("possibility clause calculation is incorrect")
		}
		if err := verifyClauseBounds(prep, routes, lower, item.FailureBounds); err != nil {
			return err
		}
		if item.MaximumPayload > maximum {
			maximum = item.MaximumPayload
		}
	}
	if err := verifyAllAssignmentCoverage(prep, seen); err != nil {
		return err
	}
	if maximum != proof.MaximumPayload {
		return invalid("possibility maximum is not the max over assignments")
	}
	return nil
}

func verifyClauseBounds(prep *prepared, routes map[PathKey]Route, lower map[string]int, clauses []ClauseBound) error {
	failureCount := 0
	for _, receipt := range prep.receipts {
		if receipt.Failed {
			failureCount++
		}
	}
	if len(clauses) != failureCount {
		return invalid("failure clause count is incorrect")
	}
	baseline := upperBoundsWithLower(prep, lower)
	for _, clause := range clauses {
		receipt := prep.receipts[clause.ReceiptIndex]
		if !receipt.Failed {
			return invalid("clause %d is not a failure receipt", clause.ReceiptIndex)
		}
		route := routes[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return invalid("clause %d overflows integer length", clause.ReceiptIndex)
		}
		if len(clause.Options) == 0 {
			return invalid("clause %d has no feasible attribution", clause.ReceiptIndex)
		}
		clauseMaximum := int(^uint(0) >> 1)
		for _, option := range clause.Options {
			found := false
			for position, item := range route.Segments {
				if item.LinkID != option.LinkID || wires[position] != option.WireLength {
					continue
				}
				found = true
				if option.WireLength <= 0 {
					return invalid("clause %d uses a non-positive wire length", clause.ReceiptIndex)
				}
				capMTU := option.WireLength - 1
				if option.CapMTU != capMTU || capMTU < lower[item.LinkID] || capMTU > prep.linkMax[item.LinkID] {
					return invalid("clause %d has an invalid MTU cap", clause.ReceiptIndex)
				}
				expectedPayload := targetPayloadWithCurrentLower(prep, assignment{chosen: routes}, baseline)
				if _, onTarget := prep.targetLinks[item.LinkID]; onTarget {
					expectedPayload = targetPayloadWithCap(prep, assignment{chosen: routes}, item.LinkID, capMTU, baseline)
				}
				if option.Payload != expectedPayload {
					return invalid("clause %d has an incorrect target payload", clause.ReceiptIndex)
				}
				if expectedPayload < clauseMaximum {
					clauseMaximum = expectedPayload
				}
			}
			if !found {
				return invalid("clause %d references an unavailable failure link", clause.ReceiptIndex)
			}
		}
		if clause.MaximumPayload != clauseMaximum {
			return invalid("clause %d maximum payload is incorrect", clause.ReceiptIndex)
		}
	}
	return nil
}

func verifyWitness(prep *prepared, witness *PossibilityWitness, payload int) error {
	routes, _, err := resolveChoices(prep, witness.PathChoices)
	if err != nil {
		return err
	}
	mtu := make(map[string]int)
	for _, bound := range witness.MTUs {
		if bound.Lower != bound.Upper {
			return invalid("witness MTU must be an exact integer")
		}
		if bound.Lower < prep.linkMin[bound.LinkID] || bound.Lower > prep.linkMax[bound.LinkID] {
			return invalid("witness MTU is outside the finite link range")
		}
		mtu[bound.LinkID] = bound.Lower
	}
	if len(mtu) != len(prep.linkMin) {
		return invalid("witness does not assign every physical link")
	}
	if err := replayWithMTUs(prep, routes, mtu); err != nil {
		return err
	}
	targetRoute, exists := routes[prep.targetKey]
	if !exists || targetRoute.ID != witness.TargetRouteID {
		return invalid("witness target route is not selected")
	}
	actual, ok := maxPayloadForRoute(targetRoute, mtu, prep.in.Context)
	if !ok || actual < payload {
		return invalid("witness cannot carry the claimed payload")
	}
	return nil
}

func replayWithMTUs(prep *prepared, routes map[PathKey]Route, mtu map[string]int) error {
	for index, receipt := range prep.receipts {
		route := routes[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return invalid("receipt %d overflows integer length", index)
		}
		failed := false
		for position, item := range route.Segments {
			if wires[position] > mtu[item.LinkID] {
				failed = true
				break
			}
		}
		if failed != receipt.Failed {
			return invalid("witness does not replay receipt %d", index)
		}
	}
	return nil
}

func replayAssignment(prep *prepared, routes map[PathKey]Route) (map[string]int, []FailureAttestation, bool) {
	lower := cloneBounds(prep.linkMin)
	failures := make([]FailureAttestation, 0)
	for index, receipt := range prep.receipts {
		route := routes[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return nil, nil, false
		}
		if !receipt.Failed {
			for position, item := range route.Segments {
				if wires[position] > prep.linkMax[item.LinkID] {
					return nil, nil, false
				}
				if wires[position] > lower[item.LinkID] {
					lower[item.LinkID] = wires[position]
				}
			}
			continue
		}
		found := false
		for position, item := range route.Segments {
			wire := wires[position]
			if wire <= 0 {
				return nil, nil, false
			}
			if wire-1 >= lower[item.LinkID] && wire-1 <= prep.linkMax[item.LinkID] {
				failures = append(failures, FailureAttestation{
					ReceiptIndex: index,
					LinkID:       item.LinkID,
					WireLength:   wire,
					LinkLower:    lower[item.LinkID],
					LinkUpper:    prep.linkMax[item.LinkID],
				})
				found = true
				break
			}
		}
		if !found {
			return nil, nil, false
		}
	}
	return lower, failures, true
}

func resolveChoices(prep *prepared, choices []PathChoice) (map[PathKey]Route, map[PathKey]struct{}, error) {
	wanted := make(map[PathKey]struct{}, len(choices))
	routes := make(map[PathKey]Route, len(choices))
	for _, choice := range choices {
		key := PathKey{Generation: choice.Generation, Label: choice.Label}
		if _, exists := wanted[key]; exists {
			return nil, nil, invalid("duplicate path choice for generation=%d label=%s", key.Generation, key.Label)
		}
		available, exists := prep.routeSets[key]
		if !exists {
			return nil, nil, invalid("proof references unknown path generation=%d label=%s", key.Generation, key.Label)
		}
		var selected Route
		found := false
		for _, route := range available {
			if route.ID == choice.RouteID {
				selected = route
				found = true
				break
			}
		}
		if !found {
			return nil, nil, invalid("proof selects unknown route %q", choice.RouteID)
		}
		wanted[key] = struct{}{}
		routes[key] = selected
	}
	for _, key := range prep.activeKeys {
		if _, exists := wanted[key]; !exists {
			return nil, nil, invalid("proof omits active path generation=%d label=%s", key.Generation, key.Label)
		}
	}
	if len(wanted) != len(prep.activeKeys) {
		return nil, nil, invalid("proof contains an inactive path")
	}
	return routes, wanted, nil
}

func verifyAllAssignmentCoverage(prep *prepared, seen map[string]struct{}) error {
	expected := make(map[string]struct{})
	var dfs func(int, string)
	dfs = func(index int, prefix string) {
		if index == len(prep.activeKeys) {
			expected[prefix] = struct{}{}
			return
		}
		key := prep.activeKeys[index]
		for _, route := range prep.routeSets[key] {
			dfs(index+1, prefix+"|"+fmt.Sprintf("%d/%s/%s", key.Generation, key.Label, route.ID))
		}
	}
	dfs(0, "")
	if len(seen) != len(expected) {
		return invalid("proof does not cover every feasible route assignment")
	}
	for key := range expected {
		if _, exists := seen[key]; !exists {
			return invalid("proof misses route assignment %s", key)
		}
	}
	return nil
}

func assignmentKey(choices []PathChoice) string {
	sorted := append([]PathChoice(nil), choices...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Generation != sorted[j].Generation {
			return sorted[i].Generation < sorted[j].Generation
		}
		return sorted[i].Label < sorted[j].Label
	})
	key := ""
	for _, choice := range sorted {
		key += fmt.Sprintf("|%d/%s/%s", choice.Generation, choice.Label, choice.RouteID)
	}
	return key
}

func linkBoundsToMap(bounds []LinkBound) map[string]int {
	out := make(map[string]int, len(bounds))
	for _, bound := range bounds {
		out[bound.LinkID] = bound.Lower
	}
	return out
}

func equalBoundMaps(left map[string]int, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func verifyFailureReasons(prep *prepared, routes map[PathKey]Route, expected []FailureAttestation, actual []FailureAttestation) error {
	if len(expected) != len(actual) {
		return invalid("failure attestation count is incorrect")
	}
	expectedByReceipt := make(map[int]FailureAttestation, len(expected))
	for _, item := range expected {
		expectedByReceipt[item.ReceiptIndex] = item
	}
	for _, item := range actual {
		want, exists := expectedByReceipt[item.ReceiptIndex]
		if !exists || want != item {
			return invalid("failure attestation for receipt %d is incorrect", item.ReceiptIndex)
		}
	}
	return nil
}

func minimumRoutePayload(checks []RoutePayload) int {
	minimum := int(^uint(0) >> 1)
	for _, check := range checks {
		if check.Payload < minimum {
			minimum = check.Payload
		}
	}
	return minimum
}

func routePayloadValue(checks []RoutePayload, routeID string) int {
	for _, check := range checks {
		if check.RouteID == routeID {
			return check.Payload
		}
	}
	return -1
}

func equalWitness(left *PossibilityWitness, right *PossibilityWitness) bool {
	if left.TargetRouteID != right.TargetRouteID || left.Payload != right.Payload {
		return false
	}
	if len(left.PathChoices) != len(right.PathChoices) || len(left.MTUs) != len(right.MTUs) {
		return false
	}
	for index := range left.PathChoices {
		if left.PathChoices[index] != right.PathChoices[index] {
			return false
		}
	}
	for index := range left.MTUs {
		if left.MTUs[index] != right.MTUs[index] {
			return false
		}
	}
	return true
}
