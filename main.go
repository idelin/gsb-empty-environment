package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func escape(b []byte) string {
	const hex = "0123456789abcdef"
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x20 && c < 0x7f && c != '"' && c != '\\' {
			sb.WriteByte(c)
		} else {
			sb.WriteString(`\x`)
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&0xf])
		}
	}
	return sb.String()
}

type jsonReport struct {
	Key           string `json:"key"`
	FirstWriteSeq uint64 `json:"first_write_seq"`
	FirstWriteVal string `json:"first_write_value"`
	LastSeq       uint64 `json:"last_seq"`
	LastValue     string `json:"last_value,omitempty"`
	Deleted       bool   `json:"deleted"`
}

func main() {
	var logs stringList
	var pattern string
	var asJSON bool
	var showCorrupt bool
	flag.Var(&logs, "log", "LevelDB log file (repeatable, applied in order)")
	flag.StringVar(&pattern, "match", "", "regular expression for keys to report (default: all)")
	flag.BoolVar(&asJSON, "json", false, "emit JSON")
	flag.BoolVar(&showCorrupt, "verbose", false, "print corruption/anomaly details to stderr")
	flag.Parse()
	logs = append(logs, flag.Args()...)
	if len(logs) == 0 {
		fmt.Fprintln(os.Stderr, "usage: walscan -log FILE [-log FILE...] [-match REGEX] [-json]")
		os.Exit(2)
	}

	var match func(string) bool
	if pattern != "" {
		re, err := regexp.Compile(pattern)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad -match pattern: %v\n", err)
			os.Exit(2)
		}
		match = re.MatchString
	} else {
		match = func(string) bool { return true }
	}

	hist := NewHistory()
	var corrupt []Corruption
	var skipped []string
	for _, path := range logs {
		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			os.Exit(1)
		}
		lr := NewLogReader(f)
		for {
			rec := lr.Next()
			if rec == nil {
				break
			}
			b, err := parseBatch(rec.Data)
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s offset %d: %v", path, rec.Offset, err))
				continue
			}
			hist.Apply(b)
		}
		for _, c := range lr.Corruptions {
			corrupt = append(corrupt, Corruption{Offset: c.Offset, Reason: path + ": " + c.Reason})
		}
		f.Close()
	}

	if showCorrupt {
		for _, c := range corrupt {
			fmt.Fprintln(os.Stderr, "corruption:", c)
		}
		for _, s := range skipped {
			fmt.Fprintln(os.Stderr, "rejected batch:", s)
		}
		for _, a := range hist.Anomalies {
			fmt.Fprintln(os.Stderr, "sequence anomaly:", a)
		}
	}

	reports := hist.Query(match)
	if asJSON {
		out := make([]jsonReport, 0, len(reports))
		for _, r := range reports {
			jr := jsonReport{Key: r.Key, LastSeq: r.Last.Seq, Deleted: r.Last.Del}
			if r.HasFirst {
				jr.FirstWriteSeq = r.FirstWrite.Seq
				jr.FirstWriteVal = string(r.FirstWrite.Value)
			}
			if !r.Last.Del {
				jr.LastValue = string(r.Last.Value)
			}
			out = append(out, jr)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(out)
		return
	}

	for _, r := range reports {
		fmt.Printf("key %q\n", escape([]byte(r.Key)))
		if r.HasFirst {
			fmt.Printf("  first-write:  seq=%d value=%q\n", r.FirstWrite.Seq, escape(r.FirstWrite.Value))
		} else {
			fmt.Printf("  first-write:  (none, only deletions seen)\n")
		}
		if r.Last.Del {
			fmt.Printf("  last-visible: DELETED at seq=%d\n", r.Last.Seq)
		} else {
			fmt.Printf("  last-visible: seq=%d value=%q\n", r.Last.Seq, escape(r.Last.Value))
		}
	}
	if len(corrupt) > 0 || len(skipped) > 0 || len(hist.Anomalies) > 0 {
		fmt.Fprintf(os.Stderr, "note: %d corrupted fragment(s), %d rejected batch(es), %d sequence anomaly(ies) (use -verbose)\n",
			len(corrupt), len(skipped), len(hist.Anomalies))
	}
}
