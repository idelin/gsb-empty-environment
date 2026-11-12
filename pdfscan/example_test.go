package pdfscan_test

import (
	"fmt"

	"example.org/netsec/q0251/pdfscan"
)

func ExampleScanBytes() {
	// A minimal document whose catalog opens an automatic Launch action.
	doc := []byte(`%PDF-1.7
1 0 obj
<< /Type /Catalog /Pages 2 0 R /OpenAction 5 0 R >>
endobj
2 0 obj
<< /Type /Pages /Kids [] /Count 0 >>
endobj
5 0 obj
<< /S /Launch /F (calc.exe) >>
endobj
xref
0 6
0000000000 65535 f 
0000000009 00000 n 
0000000064 00000 n 
0000000000 65535 f 
0000000000 65535 f 
0000000113 00000 n 
trailer
<< /Size 6 /Root 1 0 R >>
startxref
162
%%EOF
`)
	report, err := pdfscan.ScanBytes(doc)
	if err != nil {
		fmt.Println("scan error:", err)
		return
	}
	fmt.Println("blocked:", report.Blocked())
	for _, f := range report.Findings {
		fmt.Printf("object %d: %s via /%s\n", f.ObjectNum, f.Kind, f.Key)
	}
	// Output:
	// blocked: true
	// object 1: open-action via /OpenAction
	// object 5: launch via /S
}
