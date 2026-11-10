package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type traceRow struct {
	index           int
	byteValue       byte
	offPre, offPost lexState
	onPre, onPost   lexState
	noteOff, noteOn string
}

func side(pre lexState, b byte) string {
	if isInside(pre, b) {
		return "IN"
	}
	return "OUT"
}

// replay runs both lexers over the word and records every transition.
func replay(word []byte) []traceRow {
	rows := make([]traceRow, len(word))
	offState, onState := stOutside, stOutside
	for i, b := range word {
		offPre, onPre := offState, onState
		offPost := nextState(escOff, offState, b)
		onPost := nextState(escOn, onState, b)
		rows[i] = traceRow{
			index:     i,
			byteValue: b,
			offPre:    offPre,
			offPost:   offPost,
			onPre:     onPre,
			onPost:    onPost,
			noteOff:   byteNote(escOff, offPre, offPost, b),
			noteOn:    byteNote(escOn, onPre, onPost, b),
		}
		offState, onState = offPost, onPost
	}
	return rows
}

func combinedNote(row traceRow) string {
	if row.noteOff == row.noteOn {
		return row.noteOff
	}
	if row.noteOff == "" {
		row.noteOff = "-"
	}
	if row.noteOn == "" {
		row.noteOn = "-"
	}
	return fmt.Sprintf("off: %s; on: %s", row.noteOff, row.noteOn)
}

func verdict(inside bool) string {
	if inside {
		return "inside"
	}
	return "outside"
}

func divergenceDirection(row traceRow) string {
	offInside := isInside(row.offPre, row.byteValue)
	onInside := isInside(row.onPre, row.byteValue)
	switch {
	case offInside && !onInside:
		return "semicolon inside string with escapes OFF, outside with escapes ON"
	case onInside && !offInside:
		return "semicolon inside string with escapes ON, outside with escapes OFF"
	default:
		return ""
	}
}

func renderHuman(out io.Writer, result searchResult, dictionary [][]byte, maxConcat int) {
	best := result.best
	rows := replay(best.word)
	diffRow := rows[best.divergenceIndex]

	fmt.Fprintln(out, "Differential found")
	fmt.Fprintf(out, "  shortest byte string: %q (%d bytes, hex %s)\n",
		string(best.word), len(best.word), strings.ToUpper(hex.EncodeToString(best.word)))
	fmt.Fprintf(out, "  divergence at byte index %d: %s\n", best.divergenceIndex, divergenceDirection(diffRow))
	fmt.Fprintf(out, "  fragment path (%d of %d allowed concatenations):", len(best.path), maxConcat)
	for i, fragment := range best.path {
		if i == 0 {
			fmt.Fprintf(out, " %q", string(fragment))
		} else {
			fmt.Fprintf(out, " | %q", string(fragment))
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "  state trace (* marks the differential byte):")
	fmt.Fprintf(out, "    %-4s %-8s %-14s %-14s %s\n", "idx", "byte", "escapes=off", "escapes=on", "note")
	for _, row := range rows {
		marker := " "
		if row.index == best.divergenceIndex {
			marker = "*"
		}
		fmt.Fprintf(out, "  %s %-4d %-8q %-14s %-14s %s\n",
			marker, row.index, string(row.byteValue),
			fmt.Sprintf("%s->%s %s", row.offPre, row.offPost, side(row.offPre, row.byteValue)),
			fmt.Sprintf("%s->%s %s", row.onPre, row.onPost, side(row.onPre, row.byteValue)),
			combinedNote(row))
	}
	fmt.Fprintf(out, "  settled %d product states (16-state product automaton; combinations were not enumerated)\n",
		result.visited)
}

type jsonTraceRow struct {
	Index        int    `json:"index"`
	Byte         string `json:"byte"`
	EscOffSide   string `json:"esc_off_side"`
	EscOnSide    string `json:"esc_on_side"`
	Differential bool   `json:"differential"`
	Note         string `json:"note"`
}

type jsonReport struct {
	Found           bool           `json:"found"`
	Word            string         `json:"word,omitempty"`
	Hex             string         `json:"hex,omitempty"`
	Length          int            `json:"length,omitempty"`
	DivergenceIndex int            `json:"divergence_index,omitempty"`
	EscOffVerdict   string         `json:"esc_off_semicolon,omitempty"`
	EscOnVerdict    string         `json:"esc_on_semicolon,omitempty"`
	MaxConcat       int            `json:"max_concat"`
	Fragments       []string       `json:"fragments"`
	ExploredStates  int            `json:"explored_states"`
	Trace           []jsonTraceRow `json:"trace,omitempty"`
}

func renderJSON(out io.Writer, result searchResult, dictionary [][]byte, maxConcat int) error {
	fragments := make([]string, 0, len(dictionary))
	for _, fragment := range dictionary {
		fragments = append(fragments, string(fragment))
	}
	report := jsonReport{
		Found:          result.best != nil,
		MaxConcat:      maxConcat,
		Fragments:      fragments,
		ExploredStates: result.visited,
	}
	if result.best != nil {
		best := result.best
		rows := replay(best.word)
		diffRow := rows[best.divergenceIndex]
		report.Word = string(best.word)
		report.Hex = strings.ToUpper(hex.EncodeToString(best.word))
		report.Length = len(best.word)
		report.DivergenceIndex = best.divergenceIndex
		report.EscOffVerdict = verdict(isInside(diffRow.offPre, diffRow.byteValue))
		report.EscOnVerdict = verdict(isInside(diffRow.onPre, diffRow.byteValue))
		report.Trace = make([]jsonTraceRow, 0, len(rows))
		for _, row := range rows {
			report.Trace = append(report.Trace, jsonTraceRow{
				Index:        row.index,
				Byte:         string(row.byteValue),
				EscOffSide:   side(row.offPre, row.byteValue),
				EscOnSide:    side(row.onPre, row.byteValue),
				Differential: row.index == best.divergenceIndex,
				Note:         combinedNote(row),
			})
		}
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
