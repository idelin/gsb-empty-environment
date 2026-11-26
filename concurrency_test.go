package kt

import (
	"sync"
	"testing"
)

// TestConcurrentReaders exercises the RWMutex under parallel read access
// while epochs are being ingested.
func TestConcurrentReaders(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	t1 := referenceTree(t, 1, Entry{lA, leaf("pk-A", false, 0)})
	w.ingest(1, t1, &MigrationLog{Epoch: 1})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = w.verif.Locked()
				_ = w.verif.RevocCounter()
				_ = w.verif.EpochRoot(1)
			}
		}()
	}
	wg.Wait()
}

// TestEpochCapEnforced ingests up to the eighth epoch and confirms the ninth
// is refused.
func TestEpochCapEnforced(t *testing.T) {
	w := newWorld(t)
	lA := mustPrefix(t, "010001")
	for epoch := uint32(1); epoch <= MaxEpochs; epoch++ {
		tr := referenceTree(t, epoch, Entry{lA, leaf("pk-A", false, 0)})
		w.ingest(epoch, tr, &MigrationLog{Epoch: epoch})
	}
	tr9 := referenceTree(t, MaxEpochs+1, Entry{lA, leaf("pk-A", false, 0)})
	sr := signRoot(t, w.keys, MaxEpochs+1, tr9.RootHash(), 0,
		(&MigrationLog{Epoch: MaxEpochs + 1}).Hash(),
		w.roots[MaxEpochs].Body.Hash())
	err := w.verif.IngestEpoch(sr, &MigrationLog{Epoch: MaxEpochs + 1},
		w.trees[MaxEpochs], tr9)
	expectErrIs(t, err, ErrEpochRange)
}
