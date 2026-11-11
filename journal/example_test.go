package journal_test

import (
	"fmt"
	"os"
	"path/filepath"

	"example.org/offlinecli/journal"
)

func ExampleProcessor() {
	const jid uint64 = 1
	dir, err := os.MkdirTemp("", "journal-example-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	p, err := journal.OpenProcessor(filepath.Join(dir, "cp.json"), jid, 100)
	if err != nil {
		panic(err)
	}

	file := journal.FileID{RefNum: 7, Seq: 1}
	from := journal.LinkKey{ParentRef: 1, Name: "draft.txt"}
	to := journal.LinkKey{ParentRef: 1, Name: "final.txt"}

	// Records may be delivered out of order; the processor applies by USN.
	records := []journal.Record{
		{USN: 102, JournalID: jid, Reason: journal.ReasonRenameNew, ID: file, NewParent: to.ParentRef, NewName: to.Name},
		{USN: 100, JournalID: jid, Reason: journal.ReasonCreate, ID: file, ParentRef: from.ParentRef, Name: from.Name},
		{USN: 101, JournalID: jid, Reason: journal.ReasonRenameOld, ID: file, ParentRef: from.ParentRef, Name: from.Name},
	}
	if _, err := p.ApplyBatch(records); err != nil {
		panic(err)
	}

	snap := p.Snapshot()
	owner, _ := snap.LookupEntry(to)
	links, _ := snap.Links(file)
	fmt.Println(owner == file, len(links), links[0] == to)

	// Restart: state (next USN, index, unpaired halves) is on disk.
	if err := p.ValidateJournal(jid, 100); err != nil {
		panic(err)
	}
	// Output: true 1 true
}
