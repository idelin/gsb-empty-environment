package outerjoin

import "fmt"

type DegradeStep struct {
	NodePath string
	From     JoinType
	To       JoinType
	Reason   string
}

type DegradeReport struct {
	Changed bool
	Steps   []DegradeStep
}

type NullSideFacts struct {
	TargetLeftNullRejecting  bool
	TargetRightNullRejecting bool
	RejectsLeftOuterRows     bool
	RejectsRightOuterRows    bool
	LeftWitness              Row
	RightWitness             Row
}

func AnalyzeTopNullSides(query Query) (NullSideFacts, error) {
	schema := query.From.Schema()
	leftRels := []string{}
	rightRels := []string{}
	if join, ok := query.From.(*JoinNode); ok {
		leftRels = join.Left.Relations()
		rightRels = join.Right.Relations()
	} else {
		return NullSideFacts{}, nil
	}

	leftNR, leftWitness, err := NullRejectingOnSet(query.Where, schema, leftRels)
	if err != nil {
		return NullSideFacts{}, err
	}
	rightNR, rightWitness, err := NullRejectingOnSet(query.Where, schema, rightRels)
	if err != nil {
		return NullSideFacts{}, err
	}
	return NullSideFacts{
		TargetLeftNullRejecting:  leftNR,
		TargetRightNullRejecting: rightNR,
		RejectsLeftOuterRows:     rightNR,
		RejectsRightOuterRows:    leftNR,
		LeftWitness:              leftWitness,
		RightWitness:             rightWitness,
	}, nil
}

func DegradeQuery(query Query) (Query, DegradeReport, error) {
	cloned := query.Clone()
	report := DegradeReport{}
	if cloned.From == nil {
		return cloned, report, nil
	}
	schema := cloned.From.Schema()
	node, err := degradeNode(cloned.From, cloned.Where, schema, "", &report)
	if err != nil {
		return Query{}, DegradeReport{}, err
	}
	cloned.From = node
	report.Changed = len(report.Steps) > 0
	return cloned, report, nil
}

func MustDegradeQuery(query Query) Query {
	degraded, _, err := DegradeQuery(query)
	if err != nil {
		panic(err)
	}
	return degraded
}

func degradeNode(node Node, where Pred, global Schema, path string, report *DegradeReport) (Node, error) {
	join, ok := node.(*JoinNode)
	if !ok {
		return node, nil
	}
	if path == "" {
		path = "join"
	}

	leftNR, _, err := NullRejectingOnSet(where, global, join.Left.Relations())
	if err != nil {
		return nil, err
	}
	rightNR, _, err := NullRejectingOnSet(where, global, join.Right.Relations())
	if err != nil {
		return nil, err
	}

	original := join.Type
	next := original
	reason := ""
	switch join.Type {
	case Left:
		if rightNR {
			next = Inner
			reason = "top filter is null-rejecting on the right/null-extended input"
		}
	case Right:
		if leftNR {
			next = Inner
			reason = "top filter is null-rejecting on the left/null-extended input"
		}
	case Full:
		switch {
		case leftNR && rightNR:
			next = Inner
			reason = "top filter is null-rejecting on both inputs"
		case rightNR:
			next = Right
			reason = "right-target rejection removes unmatched left outer rows"
		case leftNR:
			next = Left
			reason = "left-target rejection removes unmatched right outer rows"
		}
	case Inner:
	}

	if next != original {
		join.Type = next
		report.Steps = append(report.Steps, DegradeStep{
			NodePath: path,
			From:     original,
			To:       next,
			Reason:   reason,
		})
	}

	left, err := degradeNode(join.Left, where, global, path+".left", report)
	if err != nil {
		return nil, err
	}
	right, err := degradeNode(join.Right, where, global, path+".right", report)
	if err != nil {
		return nil, err
	}
	join.Left = left
	join.Right = right
	return join, nil
}

func (f NullSideFacts) PromptMapping() string {
	switch {
	case f.TargetLeftNullRejecting && f.TargetRightNullRejecting:
		return "FULL -> INNER"
	case f.RejectsLeftOuterRows && !f.RejectsRightOuterRows:
		return "FULL -> RIGHT"
	case f.RejectsRightOuterRows && !f.RejectsLeftOuterRows:
		return "FULL -> LEFT"
	default:
		return "FULL remains FULL"
	}
}

func FormatDegradeReport(report DegradeReport) string {
	if !report.Changed {
		return "no null-rejecting degradation"
	}
	out := ""
	for _, step := range report.Steps {
		out += fmt.Sprintf("%s: %s -> %s (%s)\n", step.NodePath, step.From, step.To, step.Reason)
	}
	return out
}
