// Command timemap compresses long idle stretches of a multi-track timeline
// while protecting drag intervals and continuous audio, then prints the
// forward mapping, the compression rationale, per-track offset evidence and
// verification checks.
//
// Usage: timemap spec.json
package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"

	"timemap"
)

type eventSpec struct {
	ID string      `json:"id"`
	T  timemap.Rat `json:"t"`
}

type protectedSpec struct {
	ID    string      `json:"id"`
	Kind  string      `json:"kind"`
	Start timemap.Rat `json:"start"`
	End   timemap.Rat `json:"end"`
}

type trackSpec struct {
	ID       string      `json:"id"`
	Start    timemap.Rat `json:"start"`
	Interval timemap.Rat `json:"interval"`
}

type reverseQuery struct {
	Track string `json:"track"`
	Frame int64  `json:"frame"`
}

type fileSpec struct {
	MinIdleGap         timemap.Rat     `json:"min_idle_gap"`
	CompressedDuration timemap.Rat     `json:"compressed_duration"`
	Events             []eventSpec     `json:"events"`
	Protected          []protectedSpec `json:"protected"`
	Tracks             []trackSpec     `json:"tracks"`
	SyncPoints         []timemap.Rat   `json:"sync_points"`
	ReverseQueries     []reverseQuery  `json:"reverse_queries"`
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "timemap:", err)
	os.Exit(1)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: timemap spec.json")
		os.Exit(2)
	}
	data, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fail(err)
	}
	var fs fileSpec
	if err := json.Unmarshal(data, &fs); err != nil {
		fail(err)
	}

	spec := timemap.Spec{
		MinIdleGap:         fs.MinIdleGap,
		CompressedDuration: fs.CompressedDuration,
	}
	for _, e := range fs.Events {
		spec.Events = append(spec.Events, e.T)
	}
	for _, p := range fs.Protected {
		iv := timemap.Interval{ID: p.ID, Kind: p.Kind, Start: p.Start, End: p.End}
		if iv.Kind == "" {
			iv.Kind = "protected"
		}
		spec.Protected = append(spec.Protected, iv)
	}
	res, err := timemap.Build(spec)
	if err != nil {
		fail(err)
	}

	tracks := make([]timemap.Track, 0, len(fs.Tracks))
	for _, ts := range fs.Tracks {
		tracks = append(tracks, timemap.Track{ID: ts.ID, Start: ts.Start, Interval: ts.Interval})
	}

	fmt.Println("== Forward mapping (breakpoints) ==")
	for _, p := range res.Mapping.Points() {
		fmt.Printf("  x=%-10s -> y=%s\n", p.X, p.Y)
	}

	fmt.Println("\n== Segments (rationale) ==")
	for _, s := range res.Segments {
		fmt.Printf("  [%s, %s) -> [%s, %s)  %-15s %s\n",
			s.Start, s.End, s.MappedStart, s.MappedEnd, s.Kind, s.Reason)
	}

	fmt.Println("\n== Per-track offset evidence ==")
	ev, err := timemap.Evidence(tracks, res.Mapping)
	if err != nil {
		fail(err)
	}
	for _, e := range ev {
		fmt.Printf("  track %q: original start %s -> mapped start %s (shift %s), interval %s\n",
			e.TrackID, e.OriginalStart, e.MappedStart, e.Shift, e.Interval)
	}

	fmt.Println("\n== Verification checks ==")
	allOK := true
	for _, c := range timemap.Verify(res, spec) {
		status := "OK  "
		if !c.OK {
			status = "FAIL"
			allOK = false
		}
		fmt.Printf("  [%s] %s: %s\n", status, c.Name, c.Detail)
	}

	if len(fs.SyncPoints) > 0 {
		fmt.Println("\n== Cross-track sync points ==")
		for _, t := range fs.SyncPoints {
			sc := timemap.CheckSyncPoint(res.Mapping, tracks, t)
			status := "OK  "
			if !sc.OK {
				status = "FAIL"
				allOK = false
			}
			fmt.Printf("  [%s] %s\n", status, sc.Description)
			for _, ts := range sc.PerTrack {
				fmt.Printf("       track %q -> frame %s, bucket [%s, %s)\n",
					ts.TrackID, ts.Frame, ts.BucketLo, ts.BucketHi)
			}
		}
	}

	if len(fs.ReverseQueries) > 0 {
		fmt.Println("\n== Reverse lookup (frame -> original interval) ==")
		byID := map[string]timemap.Track{}
		for _, t := range tracks {
			byID[t.ID] = t
		}
		for _, q := range fs.ReverseQueries {
			tr, ok := byID[q.Track]
			if !ok {
				fail(fmt.Errorf("reverse query: unknown track %q", q.Track))
			}
			n := big.NewInt(q.Frame)
			lo, hi, err := tr.ReverseFrame(res.Mapping, n)
			if err != nil {
				fail(err)
			}
			fmt.Printf("  track %q frame %d -> original [%s, %s)\n", q.Track, q.Frame, lo, hi)
		}
	}

	if !allOK {
		os.Exit(1)
	}
}
