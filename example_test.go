package netsec

import "fmt"

func ExampleAnalyze() {
	program := Seq(
		Let("o", NewObject()),
		Let("p", Get(Var("o"), Lit("__proto__"))),
		SetField(Var("p"), External(), Lit("a")),
	)
	report, err := Analyze(program)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%d finding, certainty=%s, keys=%d\n",
		len(report.Findings), report.Findings[0].Certainty, len(report.Findings[0].Keys))
	// Output:
	// 1 finding, certainty=must, keys=5
}
