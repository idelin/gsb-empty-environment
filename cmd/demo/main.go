// Demo walks through the inbox crash-recovery semantics end to end:
// duplicate delivery, crash after commit, lost ack, resend with stable id.
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"inbox"
	"inbox/wal"
)

type printTransport struct{ n int }

func (t *printTransport) Send(o inbox.Output) error {
	t.n++
	fmt.Printf("  network send #%d: id=%s payload=%s\n", t.n, o.ID, o.Payload)
	return nil
}

func handler(tx inbox.Tx, ev inbox.Event) ([]inbox.Output, error) {
	raw, _ := tx.GetState("balance")
	bal, _ := strconv.Atoi(string(raw))
	delta, _ := strconv.Atoi(string(ev.Payload))
	bal += delta
	tx.SetState("balance", []byte(strconv.Itoa(bal)))
	return []inbox.Output{{Payload: []byte(fmt.Sprintf("balance=%d", bal))}}, nil
}

func main() {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	path := filepath.Join(dir, "demo.wal")
	defer os.Remove(path)

	fmt.Println("== delivery #1 (duplicate: applied once) ==")
	s, err := wal.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	p := inbox.NewProcessor(s)
	ev := inbox.Event{ID: "evt-42", Payload: []byte("100")}
	for i := 0; i < 2; i++ {
		applied, err := p.Process(ev, handler)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("  delivery %d: applied=%v\n", i+1, applied)
	}

	fmt.Println("== crash after commit, before dispatch ==")
	s.Close() // stand-in for process death; state is durable
	s, err = wal.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	tr := &printTransport{}
	d := inbox.NewDispatcher(s, tr)
	d.AfterSend = func(o inbox.Output) error {
		fmt.Println("  *** crash: ack (MarkSent) lost after send ***")
		return errors.New("crash")
	}
	d.DispatchPending()

	fmt.Println("== restart: resend with stable id ==")
	s.Close()
	s, err = wal.Open(path)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	d = inbox.NewDispatcher(s, tr)
	if _, err := d.DispatchPending(); err != nil {
		log.Fatal(err)
	}

	fmt.Println("== receiver: dedup by stable output id ==")
	recv := inbox.NewReceiver()
	// The receiver saw both network sends of evt-42#0.
	recv.Receive(inbox.Output{ID: "evt-42#0"})
	recv.Receive(inbox.Output{ID: "evt-42#0"})
	fmt.Printf("  network sends: %d, business effects applied: %d\n", tr.n, recv.Applied)
}
