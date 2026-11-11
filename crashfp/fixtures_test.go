package crashfp

const (
	digestApp   = "build-id-app-v1"
	digestLib   = "build-id-lib-v1"
	digestOther = "build-id-other-v1"
)

func makeFrame(module string, address, base uint64, digest, function, file string, line int, inlines ...InlineFrame) RawFrame {
	return RawFrame{
		Module:   module,
		Address:  address,
		Function: function,
		File:     file,
		Line:     line,
		Inlines:  inlines,
	}
}

func makeInline(function, file string, line int) InlineFrame {
	return InlineFrame{Function: function, File: file, Line: line}
}

func appLibStack(addressApp, addressLib uint64) []RawFrame {
	return []RawFrame{
		makeFrame("app", addressApp, 0x1000, digestApp, "main.worker", "main.go", 42,
			makeInline("main.dispatch", "main.go", 70),
			makeInline("main.run", "main.go", 88)),
		makeFrame("lib", addressLib, 0x4000, digestLib, "lib.Parse", "parse.c", 130),
		makeFrame("lib", addressLib+0x40, 0x4000, digestLib, "lib.Eval", "eval.c", 57),
	}
}

func modules(baseApp, baseLib uint64) []ModuleLoad {
	return []ModuleLoad{
		{Name: "app", Digest: digestApp, Base: baseApp},
		{Name: "lib", Digest: digestLib, Base: baseLib},
	}
}

func buildReports() []Report {
	truncated, err := Normalize([]RawFrame{
		makeFrame("lib", 0x4800, 0x4000, digestLib, "lib.Parse", "parse.c", 130),
		makeFrame("lib", 0x4840, 0x4000, digestLib, "lib.Eval", "eval.c", 57),
	}, modules(0x1000, 0x4000))
	if err != nil {
		panic(err)
	}
	truncated.Truncated = true

	return []Report{
		{
			ID:        "r1",
			Exception: "SIGSEGV",
			Modules:   modules(0x1000, 0x4000),
			Frames:    appLibStack(0x1200, 0x4800),
		},
		{
			ID:        "r2",
			Exception: "ACCESS_VIOLATION",
			Modules:   modules(0x9000, 0xc000),
			Frames:    appLibStack(0x9200, 0xc800),
		},
		{
			ID:        "r3",
			Exception: "SIGSEGV",
			Modules:   modules(0x5000, 0x7000),
			Frames:    appLibStack(0x5200, 0x7800),
		},
		{ID: "r4", Exception: "SIGSEGV", Stack: truncated},
		{
			ID:        "r5",
			Exception: "SIGSEGV",
			Modules:   modules(0x1000, 0x4000),
			Frames: []RawFrame{
				makeFrame("app", 0x1300, 0x1000, digestApp, "main.other", "main.go", 201),
				makeFrame("lib", 0x4800, 0x4000, digestLib, "lib.Parse", "parse.c", 130),
				makeFrame("lib", 0x4840, 0x4000, digestLib, "lib.Eval", "eval.c", 57),
			},
		},
		{
			ID:        "r6",
			Exception: "SIGABRT",
			Modules: []ModuleLoad{
				{Name: "other", Digest: digestOther, Base: 0x6000},
				{Name: "lib", Digest: digestLib, Base: 0x4000},
			},
			Frames: []RawFrame{
				makeFrame("other", 0x6100, 0x6000, digestOther, "other.Fail", "other.c", 12),
				makeFrame("lib", 0x4840, 0x4000, digestLib, "lib.Eval", "eval.c", 57),
			},
		},
		{
			ID:        "r7",
			Exception: "SIGSEGV",
			Modules:   modules(0x9000, 0xc000),
			Frames: []RawFrame{
				makeFrame("app", 0x9200, 0x9000, digestApp, "main.worker", "main.go", 42,
					makeInline("main.dispatch", "main.go", 70)),
				makeFrame("lib", 0xc800, 0xc000, digestLib, "lib.Eval", "eval.c", 57),
			},
		},
	}
}

func expectedClasses() map[string][]string {
	return map[string][]string{
		"r1": {"r1", "r2", "r3"},
		"r4": {"r4"},
		"r5": {"r5"},
		"r6": {"r6"},
		"r7": {"r7"},
	}
}

func expectedCandidates() map[[2]string]Reason {
	return map[[2]string]Reason{
		{"r1", "r4"}: ReasonTruncatedSuffix,
		{"r4", "r5"}: ReasonTruncatedSuffix,
		{"r1", "r5"}: ReasonCommonSuffix,
		{"r1", "r6"}: ReasonCommonSuffix,
		{"r4", "r6"}: ReasonCommonSuffix,
		{"r5", "r6"}: ReasonCommonSuffix,
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for index := range a {
		if a[index] != b[index] {
			return false
		}
	}
	return true
}
