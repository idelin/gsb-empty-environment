// Command demo feeds a stream to the decoder in irregular chunks (including
// a length header split across chunks), prints events as they are published,
// demonstrates an error with an absolute offset, and shows cancellation.
package main

import (
	"fmt"

	"streamdec"
)

func main() {
	// {"name":"Alice","age":30,"addr":{"city":"Shanghai"},"ok":true}
	stream := `{S4:nameS5:AliceS3:ageI30;S4:addr{S4:cityS8:Shanghai}S2:okT}`

	d := streamdec.NewDecoder()

	fmt.Println("== feed in irregular chunks (note the split length header) ==")
	chunks := []string{
		stream[:7],   // "{S4:nam" — field name split
		stream[7:20], // "eS5:AliceS3:a" — string value split
		stream[20:23],
		stream[23:42], // ends right after 'S' — the "8:" length header is split
		stream[42:],
	}
	for i, c := range chunks {
		evs, err := d.Write([]byte(c))
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		fmt.Printf("chunk %d %-20q -> %d event(s)\n", i, c, len(evs))
		for _, e := range evs {
			fmt.Printf("    %s\n", formatEvent(e))
		}
	}
	if err := d.Finish(); err != nil {
		fmt.Println("finish error:", err)
	}

	fmt.Println("\n== error reports absolute offset ==")
	d2 := streamdec.NewDecoder()
	if _, err := d2.Write([]byte("{S1:aI1;")); err != nil {
		fmt.Println("unexpected:", err)
	}
	if _, err := d2.Write([]byte("?")); err != nil {
		fmt.Println("got:", err)
	}

	fmt.Println("\n== finish with unclosed object ==")
	d3 := streamdec.NewDecoder()
	if _, err := d3.Write([]byte("{S1:aI1;")); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Println("got:", d3.Finish())

	fmt.Println("\n== consumer cancel frees the partial object ==")
	d4 := streamdec.NewDecoder()
	if _, err := d4.Write([]byte("{S4:nameS5:Al")); err != nil {
		fmt.Println("unexpected:", err)
	}
	d4.Reset() // cancel: drop cached partial object
	evs, err := d4.Write([]byte("{S1:bT}"))
	if err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Printf("after reset, fresh object published %d event(s)\n", len(evs))
}

func formatEvent(e streamdec.Event) string {
	switch e.Kind {
	case streamdec.EventFieldStart:
		return fmt.Sprintf("FieldStart  %q", e.Name)
	case streamdec.EventString:
		return fmt.Sprintf("String      %q", e.Str)
	case streamdec.EventInt:
		return fmt.Sprintf("Int         %d", e.Int)
	case streamdec.EventBool:
		return fmt.Sprintf("Bool        %v", e.Bool)
	default:
		return e.Kind.String()
	}
}
