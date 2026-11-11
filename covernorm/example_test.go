package covernorm

import "fmt"

func ExampleNormalizer() {
	normalizer := New()

	_ = normalizer.Ingest(&LoadEvent{
		Seq: 1, ConfirmedAt: 3, Module: "alpha",
		Digest:     MustParseDigest("1111111111111111111111111111111111111111111111111111111111111111"),
		Generation: 1,
		Segments:   []Segment{{Base: 0x1000, Size: 0x100, Offset: 0}},
	}, nil, nil)
	_ = normalizer.Ingest(nil, nil, &SampleEvent{Seq: 2, Thread: 1, From: 0x1010, To: 0x1020})
	_ = normalizer.Ingest(nil, nil, &SampleEvent{Seq: 4, Thread: 1, From: 0x1010, To: 0x1020})
	_ = normalizer.Ingest(nil, &UnloadEvent{
		Seq: 5, Module: "alpha",
		Digest: MustParseDigest("1111111111111111111111111111111111111111111111111111111111111111"),
	}, nil)

	archive, _ := normalizer.Flush()
	for _, line := range archive.Explain() {
		fmt.Println(line)
	}

	// Output:
	// #1   load      gen=1 module=alpha digest=11111111 segments=0x1000+0x100->0x0
	// #2   unknown   thread=1  0x00001010->0x00001020 from=before-load to=before-load
	// #4   edge      thread=1  0x00001010->0x00001020 => 11111111+0x10->11111111+0x20
	// #5   unload    module=alpha digest=11111111
}
