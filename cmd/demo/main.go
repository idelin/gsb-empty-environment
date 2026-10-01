// Command demo exercises the streamdec decoder: it frames two objects,
// feeds them to the decoder one byte at a time (proving the decoder can
// pause after any byte, including inside the length header), prints the
// events published per completed object, then shows an absolute error
// offset, consumer cancellation, and finish-with-unclosed-object handling.
package main

import (
	"fmt"

	"streamdec"
)

func main() {
	const maxObject = 1 << 20 // 1 MiB

	msg1 := streamdec.Frame([]byte(`{"user":"ada","age":36,"admin":true}`))
	msg2 := streamdec.Frame([]byte(`{"pos":{"x":1.5,"y":-2},"tag":null}`))
	stream := append(msg1, msg2...)

	d := streamdec.NewDecoder(maxObject)
	fmt.Println("== feeding two framed objects, one byte at a time ==")
	obj := 0
	for i := 0; i < len(stream); i++ {
		events, err := d.Feed(stream[i : i+1])
		if err != nil {
			fmt.Println("error:", err)
			return
		}
		if len(events) > 0 { // only published once the object is complete
			obj++
			fmt.Printf("-- object %d complete, %d events published --\n", obj, len(events))
			for _, ev := range events {
				fmt.Println("  ", ev)
			}
		}
	}
	if err := d.Finish(); err != nil {
		fmt.Println("finish:", err)
	}

	fmt.Println("\n== error offsets are absolute across messages ==")
	bad := append(streamdec.Frame([]byte(`{"ok":1}`)), streamdec.Frame([]byte(`{"bad": @}`))...)
	d = streamdec.NewDecoder(maxObject)
	if _, err := d.Feed(bad); err != nil {
		fmt.Println("  ", err)
	}

	fmt.Println("\n== consumer cancel releases the current object cache ==")
	d = streamdec.NewDecoder(maxObject)
	partial := streamdec.Frame([]byte(`{"big":"never finished`))
	if _, err := d.Feed(partial[:len(partial)-3]); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Printf("   mid-object, offset=%d -> Cancel()\n", d.Offset())
	d.Cancel()
	fmt.Printf("   after cancel, offset=%d (cache released, decoder reusable)\n", d.Offset())

	fmt.Println("\n== finish with an unclosed object reports an error ==")
	d = streamdec.NewDecoder(maxObject)
	if _, err := d.Feed(partial[:len(partial)-3]); err != nil {
		fmt.Println("unexpected:", err)
	}
	fmt.Println("  ", d.Finish())
}
