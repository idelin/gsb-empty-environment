package crashfp_test

import (
	"fmt"

	"example.org/netsec/q0409/crashfp"
)

func Example() {
	// The same binary loaded at two unrelated bases (ASLR).
	firstModules := []crashfp.ModuleLoad{
		{Name: "app", Digest: "build-id-42", Base: 0x100000},
		{Name: "libc", Digest: "build-id-libc", Base: 0x400000},
	}
	secondModules := []crashfp.ModuleLoad{
		{Name: "app", Digest: "build-id-42", Base: 0x900000},
		{Name: "libc", Digest: "build-id-libc", Base: 0xc00000},
	}

	firstFrames := []crashfp.RawFrame{
		{
			Module:   "app",
			Address:  0x100420,
			Function: "main.handle",
			File:     "main.go",
			Line:     33,
			Inlines: []crashfp.InlineFrame{
				{Function: "main.dispatch", File: "main.go", Line: 51},
			},
		},
		{
			Module:   "libc",
			Address:  0x400810,
			Function: "libc.abort",
			File:     "abort.c",
			Line:     12,
		},
	}
	secondFrames := []crashfp.RawFrame{
		{
			Module:   "app",
			Address:  0x900420,
			Function: "main.handle",
			File:     "main.go",
			Line:     33,
			Inlines: []crashfp.InlineFrame{
				{Function: "main.dispatch", File: "main.go", Line: 51},
			},
		},
		{
			Module:   "libc",
			Address:  0xc00810,
			Function: "libc.abort",
			File:     "abort.c",
			Line:     12,
		},
	}

	classifier := crashfp.NewClassifier(nil)
	reports := []crashfp.Report{
		{ID: "crash-A", Exception: "SIGABRT", Modules: firstModules, Frames: firstFrames},
		{ID: "crash-B", Exception: "fatal error", Modules: secondModules, Frames: secondFrames},
	}

	// Insertion order is irrelevant; try either order.
	if err := classifier.Add(reports[1]); err != nil {
		panic(err)
	}
	if err := classifier.Add(reports[0]); err != nil {
		panic(err)
	}

	for _, class := range classifier.Classes() {
		fmt.Printf("class %s -> %v\n", class.ID, class.ReportIDs)
	}

	// Output:
	// class crash-A -> [crash-A crash-B]
}
