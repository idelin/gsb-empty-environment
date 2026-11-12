package netsec

import (
	"fmt"
)

func ExampleAnalyze() {
	libraries := Libraries{
		"ctcmp": {
			Name: "ctcmp",
			Params: []ParamSummary{{
				TimingBits: 0xff,
			}},
		},
	}
	builder := NewBuilder("example")
	secret := builder.Secret("secret", 8)
	mask := builder.Const("mask", 8, 0x0f)
	masked := builder.And("masked", secret, mask)
	builder.Call("compare", CallSpec{
		Library:       "ctcmp",
		AllowedPublic: []Mask{0x0f},
	}, masked)
	program := builder.Build()

	report, err := Analyze(program, libraries)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.Findings)
	// Output: []
}
