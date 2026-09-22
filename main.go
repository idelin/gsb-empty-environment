package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type Evidence struct {
	OriginalSafetyFailure *Report          `json:"original_safety_failure"`
	Reduction             *ReductionResult `json:"reduction"`
	LivenessControl       *Report          `json:"liveness_control"`
	LivenessVerdict       string           `json:"liveness_verdict"`
	NoDropControl         *Report          `json:"no_drop_control"`
	NoDropVerdict         string           `json:"no_drop_verdict"`
	DependencyRejection   string           `json:"dependency_rejected_candidate_error"`
}

func main() {
	jsonOutput := flag.Bool("json", false, "emit machine-readable evidence as JSON")
	flag.Parse()

	events := safetySample()
	original, err := Replay(events)
	must(err)
	if original.FailureClass() != "safety" {
		must(fmt.Errorf("sample did not start with a safety failure"))
	}
	reduction, err := MinimizeCounterexample(events)
	must(err)

	liveness, err := Replay(livenessSample())
	must(err)
	noDropEvents := append([]Event(nil), reduction.Minimal...)
	noDropEvents = removeEvent(noDropEvents, "drop-x")
	noDropReport, err := Replay(noDropEvents)
	noDropVerdict := "rejected"
	if err == nil {
		noDropVerdict = noDropReport.FailureClass()
	}

	invalidCandidate := removeEvent(events, "deliver-x")
	_, invalidErr := Replay(invalidCandidate)
	dependencyError := ""
	if invalidErr != nil {
		dependencyError = invalidErr.Error()
	}

	evidence := Evidence{
		OriginalSafetyFailure: original,
		Reduction:             reduction,
		LivenessControl:       liveness,
		LivenessVerdict:       liveness.FailureClass(),
		NoDropControl:         noDropReport,
		NoDropVerdict:         noDropVerdict,
		DependencyRejection:   dependencyError,
	}
	if *jsonOutput {
		encoded := json.NewEncoder(os.Stdout)
		encoded.SetIndent("", "  ")
		must(encoded.Encode(evidence))
		return
	}

	printText(evidence)
}

func printText(evidence Evidence) {
	r := evidence.Reduction
	fmt.Println("== 初始一致性失败 ==")
	printViolations("safety", evidence.OriginalSafetyFailure.Safety)
	fmt.Printf("初始事件数: %d\n", len(r.Original))
	fmt.Println()

	fmt.Println("== ddmin 缩减过程 ==")
	for i, trial := range r.Trials {
		fmt.Printf("%02d [n=%d] 删除 %v => %s", i+1, trial.Granularity, trial.Removed, trial.Verdict)
		if trial.Reason != "" {
			fmt.Printf(" (%s)", trial.Reason)
		}
		fmt.Println()
	}
	fmt.Println()

	fmt.Println("== 单事件必要性审计 ==")
	for _, audit := range r.NecessityAudit {
		fmt.Printf("删除 %s => %s", audit.EventID, audit.Verdict)
		if audit.Reason != "" {
			fmt.Printf(" (%s)", audit.Reason)
		}
		fmt.Println()
	}
	fmt.Println()

	fmt.Println("== 可重放最小反例 ==")
	for _, ev := range r.Minimal {
		fmt.Printf("- %s: %s node=%s", ev.ID, ev.Type, ev.Node)
		if ev.OpID != "" {
			fmt.Printf(" op=%s", ev.OpID)
		}
		if ev.MsgID != "" {
			fmt.Printf(" msg=%s", ev.MsgID)
		}
		if ev.Key != "" {
			fmt.Printf(" key=%s", ev.Key)
		}
		if ev.Value != "" {
			fmt.Printf(" value=%s", ev.Value)
		}
		if ev.Expected != "" {
			fmt.Printf(" expected=%s", ev.Expected)
		}
		fmt.Println()
	}
	fmt.Println()
	fmt.Println("== 最小反例安全证据 ==")
	printViolations("safety", r.FinalReport.Safety)
	fmt.Printf("B.k=%q，A.k=%q；m-x=%v\n", r.FinalReport.Replicas[NodeB].Store["k"], r.FinalReport.Replicas[NodeA].Store["k"], r.FinalReport.Messages["m-x"])
	fmt.Println()

	fmt.Println("== 对照组 ==")
	fmt.Printf("去掉必要丢包 drop-x: %s（安全错误未被错误保留）\n", evidence.NoDropVerdict)
	fmt.Printf("删除 drop 的投递事件 deliver-x: rejected (%s)\n", evidence.DependencyRejection)
	fmt.Printf("超时/崩溃序列: %s\n", evidence.LivenessVerdict)
	printViolations("liveness", evidence.LivenessControl.Liveness)
}

func printViolations(kind string, violations []Violation) {
	if len(violations) == 0 {
		fmt.Printf("%s: none\n", kind)
		return
	}
	for _, violation := range violations {
		fmt.Printf("%s: %s op=%s node=%s key=%s expected=%q got=%q at=%d\n",
			kind, violation.Kind, violation.OpID, violation.Node, violation.Key,
			violation.Expected, violation.Got, violation.At)
	}
}

func removeEvent(events []Event, id string) []Event {
	result := make([]Event, 0, len(events))
	for _, ev := range events {
		if ev.ID != id {
			result = append(result, ev)
		}
	}
	return result
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
