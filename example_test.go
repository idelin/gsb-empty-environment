package netsec

import "fmt"

func ExampleMapPart() {
	// quoted-printable 叶节点：含一个十六进制转义 "=E9" 与一处软换行。
	raw := []byte("caf=E9=\r\nna=C3=AFf")

	// 签名只覆盖到原始偏移 7（软换行之前），后续片段未认证。
	authenticated := []Range{{Start: 0, End: 8}}

	m, err := MapPart(raw, EncodingQuotedPrintable, authenticated)
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Printf("decoded: %q\n", m.Decoded)
	for _, span := range m.RuneRanges() {
		mark := "untrusted"
		if span.Trusted {
			mark = "trusted"
		}
		fmt.Printf("%s: %q\n", mark, m.Decoded[span.Start:span.End])
	}
	// Output:
	// decoded: "caf\xe9naïf"
	// trusted: "caf\xe9"
	// untrusted: "naïf"
}
