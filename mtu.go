package mtuinfer

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	ErrInvalidInput         = errors.New("invalid MTU inference input")
	ErrInconsistentEvidence = errors.New("inconsistent MTU evidence")
)

type Segment struct {
	Header         int
	InnerPadding   int
	OuterAlignment int
}

type RouteSegment struct {
	LinkID  string
	Segment Segment
}

type Route struct {
	ID       string
	Segments []RouteSegment
}

type PathKey struct {
	Generation int
	Label      string
}

type LinkSpec struct {
	ID     string
	MinMTU int
	MaxMTU int
}

type SendContext struct {
	MinPayloadAlignment int
	Header              int
	OuterAlignment      int
}

type Receipt struct {
	Generation int
	Label      string
	Candidates []string
	SendLength int
	Failed     bool
}

type TargetRoute struct {
	Generation int
	Label      string
	RouteID    string
}

type Input struct {
	Links    []LinkSpec
	Context  SendContext
	Routes   map[PathKey][]Route
	Receipts []Receipt
	Target   TargetRoute
}

type LinkBound struct {
	LinkID string
	Lower  int
	Upper  int
}

type PathChoice struct {
	Generation int
	Label      string
	RouteID    string
}

type RoutePayload struct {
	RouteID string
	Payload int
}

type FailureAttestation struct {
	ReceiptIndex int
	LinkID       string
	WireLength   int
	LinkLower    int
	LinkUpper    int
}

type AssignmentCertificate struct {
	PathChoices       []PathChoice
	SuccessLower      []LinkBound
	FailureReasons    []FailureAttestation
	GuaranteedMTUs    []LinkBound
	TargetRouteID     string
	GuaranteedPayload int
}

type UniversalProof struct {
	Assignments       []AssignmentCertificate
	GuaranteedPayload int
}

type OptionBound struct {
	LinkID     string
	WireLength int
	CapMTU     int
	Payload    int
}

type ClauseBound struct {
	ReceiptIndex   int
	Options        []OptionBound
	MaximumPayload int
}

type AssignmentPossibility struct {
	Certificate    AssignmentCertificate
	FailureBounds  []ClauseBound
	MaximumPayload int
}

type PossibilityWitness struct {
	PathChoices   []PathChoice
	MTUs          []LinkBound
	TargetRouteID string
	Payload       int
}

type PossibilityProof struct {
	Assignments    []AssignmentPossibility
	MaximumPayload int
	Witness        *PossibilityWitness
}

type PayloadResult struct {
	Payload          int
	UniversalProof   *UniversalProof
	PossibilityProof *PossibilityProof
	Witness          *PossibilityWitness
}

type Result struct {
	Compatible bool
	Guaranteed PayloadResult
	Possible   PayloadResult
}

type prepared struct {
	in          Input
	linkMin     map[string]int
	linkMax     map[string]int
	routeSets   map[PathKey][]Route
	receipts    []Receipt
	activeKeys  []PathKey
	targetKey   PathKey
	targetLinks map[string]struct{}
}

type assignment struct {
	chosen map[PathKey]Route
}

type fixedResult struct {
	cert       AssignmentCertificate
	lower      map[string]int
	maxPayload int
	failures   []FailureAttestation
}

func alignUp(value int, alignment int) int {
	if alignment <= 1 || value%alignment == 0 {
		return value
	}
	if value > int(^uint(0)>>1)-alignment {
		return -1
	}
	return value + alignment - (value % alignment)
}

func transformLength(input int, segment Segment) (int, bool) {
	sum := addInt(input, segment.InnerPadding)
	if sum < 0 {
		return 0, false
	}
	sum = addInt(sum, segment.Header)
	if sum < 0 {
		return 0, false
	}
	out := alignUp(sum, segment.OuterAlignment)
	if out < 0 {
		return 0, false
	}
	return out, true
}

func inverseTransform(output int, segment Segment) (int, bool) {
	if output < 0 {
		return -1, false
	}
	alignedOutput := output - output%segment.OuterAlignment
	minimum := alignUp(segment.InnerPadding+segment.Header, segment.OuterAlignment)
	if alignedOutput < minimum {
		return -1, false
	}
	input := alignedOutput - segment.Header - segment.InnerPadding
	if input < 0 {
		return -1, false
	}
	return input, true
}

func addInt(values ...int) int {
	total := 0
	for _, value := range values {
		if value < 0 {
			return -1
		}
		if value > 0 && total > int(^uint(0)>>1)-value {
			return -1
		}
		total += value
	}
	return total
}

func routeWires(start int, route Route) ([]int, bool) {
	wires := make([]int, 0, len(route.Segments))
	current := start
	for _, item := range route.Segments {
		next, ok := transformLength(current, item.Segment)
		if !ok {
			return nil, false
		}
		wires = append(wires, next)
		current = next
	}
	return wires, true
}

func maxPayloadForRoute(route Route, mtu map[string]int, context SendContext) (int, bool) {
	limit := int(^uint(0) >> 1)
	for _, item := range route.Segments {
		value, ok := mtu[item.LinkID]
		if !ok {
			return -1, false
		}
		if value < limit {
			limit = value
		}
	}
	current := limit
	for index := len(route.Segments) - 1; index >= 0; index-- {
		item := route.Segments[index]
		next, ok := inverseTransform(current, item.Segment)
		if !ok {
			return -1, false
		}
		current = next
	}
	current, ok := inverseContext(current, context)
	if !ok {
		return -1, false
	}
	return current, true
}

func inverseContext(output int, context SendContext) (int, bool) {
	if output < 0 {
		return -1, false
	}
	beforeHeader := output - context.Header
	if beforeHeader < 0 {
		return -1, false
	}
	alignment := context.MinPayloadAlignment
	return beforeHeader / alignment * alignment, true
}

func Analyze(_ context.Context, in Input) (Result, error) {
	prep, err := prepare(in)
	if err != nil {
		return Result{}, err
	}

	universal := PayloadResult{Payload: int(^uint(0) >> 1)}
	existential := PayloadResult{Payload: -1}
	certs := make([]AssignmentCertificate, 0)
	possibilities := make([]AssignmentPossibility, 0)
	var bestWitness *PossibilityWitness

	err = enumerateAssignments(prep, func(a assignment) error {
		fixed, ok := analyzeFixed(prep, a)
		if !ok {
			return nil
		}
		certs = append(certs, fixed.cert)
		possibility := analyzePossibility(prep, a, fixed)
		possibilities = append(possibilities, possibility)

		if fixed.maxPayload < universal.Payload {
			universal.Payload = fixed.maxPayload
		}
		if possibility.MaximumPayload > existential.Payload {
			existential.Payload = possibility.MaximumPayload
			bestWitness = possibilityWitness(prep, a, fixed, possibility)
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	if len(certs) == 0 {
		return Result{Compatible: false}, ErrInconsistentEvidence
	}

	universal.UniversalProof = &UniversalProof{
		Assignments:       certs,
		GuaranteedPayload: universal.Payload,
	}
	existential.PossibilityProof = &PossibilityProof{
		Assignments:    possibilities,
		MaximumPayload: existential.Payload,
		Witness:        bestWitness,
	}
	existential.Witness = bestWitness

	return Result{
		Compatible: true,
		Guaranteed: universal,
		Possible:   existential,
	}, nil
}

func enumerateAssignments(prep *prepared, visit func(assignment) error) error {
	chosen := make(map[PathKey]Route, len(prep.activeKeys))
	var dfs func(int) error
	dfs = func(index int) error {
		if index == len(prep.activeKeys) {
			return visit(assignment{chosen: chosen})
		}
		key := prep.activeKeys[index]
		for _, route := range prep.routeSets[key] {
			chosen[key] = route
			if err := dfs(index + 1); err != nil {
				return err
			}
		}
		return nil
	}
	return dfs(0)
}

func analyzeFixed(prep *prepared, a assignment) (fixedResult, bool) {
	lower := cloneBounds(prep.linkMin)
	successBounds := make([]LinkBound, 0)

	for _, receipt := range prep.receipts {
		if receipt.Failed {
			continue
		}
		route := a.chosen[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return fixedResult{}, false
		}
		for position, item := range route.Segments {
			required := wires[position]
			if required > prep.linkMax[item.LinkID] || required < 0 {
				return fixedResult{}, false
			}
			if required > lower[item.LinkID] {
				lower[item.LinkID] = required
			}
			successBounds = append(successBounds, LinkBound{
				LinkID: item.LinkID,
				Lower:  required,
				Upper:  prep.linkMax[item.LinkID],
			})
		}
	}

	failures := make([]FailureAttestation, 0)
	for index, receipt := range prep.receipts {
		if !receipt.Failed {
			continue
		}
		route := a.chosen[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			return fixedResult{}, false
		}
		found := false
		for position, item := range route.Segments {
			wire := wires[position]
			if wire <= 0 {
				continue
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
			return fixedResult{}, false
		}
	}

	targetRoutes := selectedTargetRoutes(prep, a)
	maximum := int(^uint(0) >> 1)
	var targetRouteID string
	for _, route := range targetRoutes {
		payload, ok := maxPayloadForRoute(route, lower, prep.in.Context)
		if !ok {
			return fixedResult{}, false
		}
		if payload < maximum {
			maximum = payload
			targetRouteID = route.ID
		}
	}

	sort.Slice(successBounds, func(i, j int) bool { return successBounds[i].LinkID < successBounds[j].LinkID })
	cert := AssignmentCertificate{
		PathChoices:       pathChoices(prep, a),
		SuccessLower:      successBounds,
		FailureReasons:    failures,
		GuaranteedMTUs:    boundsFromMap(lower, prep.linkMax),
		TargetRouteID:     targetRouteID,
		GuaranteedPayload: maximum,
	}
	return fixedResult{
		cert:       cert,
		lower:      lower,
		maxPayload: maximum,
		failures:   failures,
	}, true
}

func analyzePossibility(prep *prepared, a assignment, fixed fixedResult) AssignmentPossibility {
	clauses := make([]ClauseBound, 0)
	baseline := upperBoundsWithLower(prep, fixed.lower)
	assignmentMaximum := targetPayloadWithCurrentLower(prep, a, baseline)

	for receiptIndex, receipt := range prep.receipts {
		if !receipt.Failed {
			continue
		}
		route := a.chosen[PathKey{Generation: receipt.Generation, Label: receipt.Label}]
		wires, ok := routeWires(receipt.SendLength, route)
		if !ok {
			continue
		}
		options := make([]OptionBound, 0)
		clauseMaximum := assignmentMaximum
		for position, item := range route.Segments {
			wire := wires[position]
			if wire <= 0 {
				continue
			}
			capMTU := wire - 1
			if capMTU < fixed.lower[item.LinkID] || capMTU > prep.linkMax[item.LinkID] {
				continue
			}
			optionMaximum := assignmentMaximum
			if _, onTarget := prep.targetLinks[item.LinkID]; onTarget {
				optionMaximum = targetPayloadWithCap(prep, a, item.LinkID, capMTU, baseline)
			}
			options = append(options, OptionBound{
				LinkID:     item.LinkID,
				WireLength: wire,
				CapMTU:     capMTU,
				Payload:    optionMaximum,
			})
			if optionMaximum < clauseMaximum {
				clauseMaximum = optionMaximum
			}
		}
		sort.Slice(options, func(i, j int) bool {
			if options[i].LinkID != options[j].LinkID {
				return options[i].LinkID < options[j].LinkID
			}
			return options[i].WireLength < options[j].WireLength
		})
		clauses = append(clauses, ClauseBound{
			ReceiptIndex:   receiptIndex,
			Options:        options,
			MaximumPayload: clauseMaximum,
		})
		if clauseMaximum < assignmentMaximum {
			assignmentMaximum = clauseMaximum
		}
	}

	return AssignmentPossibility{
		Certificate:    fixed.cert,
		FailureBounds:  clauses,
		MaximumPayload: assignmentMaximum,
	}
}

func upperBoundsWithLower(prep *prepared, lower map[string]int) map[string]int {
	out := make(map[string]int, len(lower))
	for id, value := range lower {
		if prep.linkMax[id] > value {
			out[id] = prep.linkMax[id]
		} else {
			out[id] = value
		}
	}
	return out
}

func targetPayloadWithCurrentLower(prep *prepared, a assignment, lower map[string]int) int {
	targetRoutes := selectedTargetRoutes(prep, a)
	maximum := int(^uint(0) >> 1)
	for _, route := range targetRoutes {
		payload, ok := maxPayloadForRoute(route, lower, prep.in.Context)
		if !ok {
			return -1
		}
		if payload < maximum {
			maximum = payload
		}
	}
	return maximum
}

func targetPayloadWithCap(prep *prepared, a assignment, cappedLink string, capMTU int, lower map[string]int) int {
	targetRoutes := selectedTargetRoutes(prep, a)
	answer := int(^uint(0) >> 1)
	for _, route := range targetRoutes {
		effective := cloneBounds(lower)
		if _, usesLink := routeLinkSet(route)[cappedLink]; usesLink {
			effective[cappedLink] = capMTU
		}
		payload, ok := maxPayloadForRoute(route, effective, prep.in.Context)
		if !ok || payload < answer {
			if !ok {
				return -1
			}
			answer = payload
		}
	}
	return answer
}

func possibilityWitness(prep *prepared, a assignment, fixed fixedResult, possibility AssignmentPossibility) *PossibilityWitness {
	payload := possibility.MaximumPayload
	if payload < 0 {
		return nil
	}
	mtu := upperBoundsWithLower(prep, fixed.lower)
	for _, clause := range possibility.FailureBounds {
		var selected *OptionBound
		for index := range clause.Options {
			option := &clause.Options[index]
			if option.Payload >= payload && (selected == nil || option.CapMTU < selected.CapMTU) {
				selected = option
			}
		}
		if selected == nil {
			return nil
		}
		if current, exists := mtu[selected.LinkID]; !exists || selected.CapMTU < current {
			mtu[selected.LinkID] = selected.CapMTU
		}
	}
	targetRoute := a.chosen[prep.targetKey]
	return &PossibilityWitness{
		PathChoices:   pathChoices(prep, a),
		MTUs:          exactBoundsFromMap(mtu),
		TargetRouteID: targetRoute.ID,
		Payload:       payload,
	}
}

func selectedTargetRoutes(prep *prepared, a assignment) []Route {
	if prep.in.Target.RouteID != "" {
		for _, route := range prep.routeSets[prep.targetKey] {
			if route.ID == prep.in.Target.RouteID {
				return []Route{route}
			}
		}
	}
	return []Route{a.chosen[prep.targetKey]}
}

func routeLinkSet(route Route) map[string]struct{} {
	out := make(map[string]struct{})
	for _, item := range route.Segments {
		out[item.LinkID] = struct{}{}
	}
	return out
}

func pathChoices(prep *prepared, a assignment) []PathChoice {
	out := make([]PathChoice, 0, len(prep.activeKeys))
	for _, key := range prep.activeKeys {
		out = append(out, PathChoice{
			Generation: key.Generation,
			Label:      key.Label,
			RouteID:    a.chosen[key].ID,
		})
	}
	return out
}

func boundsFromMap(lower map[string]int, upper map[string]int) []LinkBound {
	ids := make([]string, 0, len(lower))
	for id := range lower {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]LinkBound, 0, len(ids))
	for _, id := range ids {
		out = append(out, LinkBound{LinkID: id, Lower: lower[id], Upper: upper[id]})
	}
	return out
}

func exactBoundsFromMap(values map[string]int) []LinkBound {
	ids := make([]string, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]LinkBound, 0, len(ids))
	for _, id := range ids {
		out = append(out, LinkBound{LinkID: id, Lower: values[id], Upper: values[id]})
	}
	return out
}

func cloneBounds(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func routeIDs(routes []Route) []string {
	out := make([]string, 0, len(routes))
	for _, route := range routes {
		out = append(out, route.ID)
	}
	sort.Strings(out)
	return out
}

func sameStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}

func containsRoute(routes []Route, id string) bool {
	for _, route := range routes {
		if route.ID == id {
			return true
		}
	}
	return false
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

func prepare(in Input) (*prepared, error) {
	if in.Context.MinPayloadAlignment <= 0 || in.Context.OuterAlignment <= 0 {
		return nil, invalid("context alignment must be positive")
	}
	if in.Context.Header < 0 {
		return nil, invalid("context header cannot be negative")
	}
	if len(in.Links) == 0 {
		return nil, invalid("at least one link is required")
	}

	linkMin := make(map[string]int, len(in.Links))
	linkMax := make(map[string]int, len(in.Links))
	for _, link := range in.Links {
		if link.ID == "" {
			return nil, invalid("link ID cannot be empty")
		}
		if _, exists := linkMin[link.ID]; exists {
			return nil, invalid("duplicate link ID %q", link.ID)
		}
		if link.MinMTU < 0 || link.MaxMTU < link.MinMTU {
			return nil, invalid("invalid MTU range for link %q", link.ID)
		}
		linkMin[link.ID] = link.MinMTU
		linkMax[link.ID] = link.MaxMTU
	}

	if len(in.Routes) == 0 {
		return nil, invalid("at least one route set is required")
	}
	routeSets := make(map[PathKey][]Route, len(in.Routes))
	for key, routes := range in.Routes {
		if len(routes) == 0 || len(routes) > 4 {
			return nil, invalid("path generation=%d label=%s must contain one to four routes", key.Generation, key.Label)
		}
		seen := make(map[string]struct{}, len(routes))
		validated := make([]Route, 0, len(routes))
		for _, route := range routes {
			if route.ID == "" {
				return nil, invalid("route ID cannot be empty")
			}
			if _, exists := seen[route.ID]; exists {
				return nil, invalid("duplicate route ID %q", route.ID)
			}
			seen[route.ID] = struct{}{}
			if len(route.Segments) == 0 || len(route.Segments) > 12 {
				return nil, invalid("route %q must contain one to twelve segments", route.ID)
			}
			for _, item := range route.Segments {
				if _, exists := linkMin[item.LinkID]; !exists {
					return nil, invalid("route %q references unknown link %q", route.ID, item.LinkID)
				}
				if err := validateSegment(item.Segment); err != nil {
					return nil, fmt.Errorf("%w: route %q: %v", ErrInvalidInput, route.ID, err)
				}
			}
			validated = append(validated, route)
		}
		sort.Slice(validated, func(i, j int) bool { return validated[i].ID < validated[j].ID })
		routeSets[key] = validated
	}

	receipts, err := deduplicateReceipts(in.Receipts)
	if err != nil {
		return nil, err
	}
	active := make(map[PathKey]struct{})
	for index, receipt := range receipts {
		key := PathKey{Generation: receipt.Generation, Label: receipt.Label}
		routes, exists := routeSets[key]
		if !exists {
			return nil, invalid("receipt %d references unknown route set", index)
		}
		if receipt.SendLength < 0 {
			return nil, invalid("receipt %d has negative send length", index)
		}
		if len(receipt.Candidates) == 0 || len(receipt.Candidates) > 4 {
			return nil, invalid("receipt %d must contain one to four candidates", index)
		}
		if !sameStringSet(receipt.Candidates, routeIDs(routes)) {
			return nil, invalid("receipt %d candidate set is not bound to route set generation=%d label=%s", index, key.Generation, key.Label)
		}
		active[key] = struct{}{}
	}

	targetKey := PathKey{Generation: in.Target.Generation, Label: in.Target.Label}
	targetRoutes, exists := routeSets[targetKey]
	if !exists {
		return nil, invalid("target route set generation=%d label=%s does not exist", targetKey.Generation, targetKey.Label)
	}
	if in.Target.RouteID != "" && !containsRoute(targetRoutes, in.Target.RouteID) {
		return nil, invalid("target route %q does not exist", in.Target.RouteID)
	}
	active[targetKey] = struct{}{}

	keys := make([]PathKey, 0, len(active))
	for key := range active {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Generation != keys[j].Generation {
			return keys[i].Generation < keys[j].Generation
		}
		return keys[i].Label < keys[j].Label
	})

	targetLinks := make(map[string]struct{})
	for _, route := range targetRoutes {
		for _, item := range route.Segments {
			targetLinks[item.LinkID] = struct{}{}
		}
	}

	return &prepared{
		in:          in,
		linkMin:     linkMin,
		linkMax:     linkMax,
		routeSets:   routeSets,
		receipts:    receipts,
		activeKeys:  keys,
		targetKey:   targetKey,
		targetLinks: targetLinks,
	}, nil
}

func validateSegment(segment Segment) error {
	if segment.Header < 0 || segment.InnerPadding < 0 {
		return errors.New("header and padding cannot be negative")
	}
	if segment.OuterAlignment <= 0 {
		return errors.New("outer alignment must be positive")
	}
	return nil
}

func deduplicateReceipts(input []Receipt) ([]Receipt, error) {
	seen := make(map[string]struct{})
	out := make([]Receipt, 0, len(input))
	for index, receipt := range input {
		if len(receipt.Candidates) > 4 {
			return nil, invalid("receipt %d has more than four candidates", index)
		}
		candidates := append([]string(nil), receipt.Candidates...)
		sort.Strings(candidates)
		key := fmt.Sprintf("%d|%s|%s|%d|%t", receipt.Generation, receipt.Label, strings.Join(candidates, ","), receipt.SendLength, receipt.Failed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		receipt.Candidates = candidates
		out = append(out, receipt)
	}
	return out, nil
}
