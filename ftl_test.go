package ftl

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func testConfig() Config {
	return Config{
		BlockCount:         8,
		PagesPerBlock:      8,
		PageSize:           64,
		ReservedFreeBlocks: 1,
		WearLevelThreshold: 2,
	}
}

func payload(seed int) []byte {
	data := make([]byte, 32)
	for i := range data {
		data[i] = byte(seed*31 + i*7)
	}
	return data
}

func mustWrite(t *testing.T, d *Device, lba int, data []byte) {
	t.Helper()
	if err := d.Write(lba, data); err != nil {
		t.Fatalf("write lba %d: %v", lba, err)
	}
}

func mustEqual(t *testing.T, d *Device, lba int, want []byte) {
	t.Helper()
	got, err := d.Read(lba)
	if err != nil {
		t.Fatalf("read lba %d: %v", lba, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("lba %d mismatch: got %x want %x", lba, got, want)
	}
}

func TestFlashHardwareConstraints(t *testing.T) {
	f, err := NewFlash(Config{BlockCount: 2, PagesPerBlock: 4, PageSize: 32})
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 32)
	oob := make([]byte, pageMetaSize)

	if err := f.Program(0, 1, data, oob); !errors.Is(err, ErrNotSequential) {
		t.Fatalf("out-of-order program err = %v, want %v", err, ErrNotSequential)
	}
	if err := f.Program(0, 0, data, oob); err != nil {
		t.Fatal(err)
	}
	if err := f.Program(0, 0, data, oob); !errors.Is(err, ErrNotSequential) {
		t.Fatalf("reprogram err = %v, want %v", err, ErrNotSequential)
	}
	if err := f.Program(0, 2, data, oob); !errors.Is(err, ErrNotSequential) {
		t.Fatalf("skip-page err = %v, want %v", err, ErrNotSequential)
	}

	for page := 1; page < 4; page++ {
		if err := f.Program(0, page, data, oob); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Program(0, 3, data, oob); !errors.Is(err, ErrBlockFull) {
		t.Fatalf("full block err = %v, want %v", err, ErrBlockFull)
	}
	if got := f.WritePointer(0); got != 4 {
		t.Fatalf("write pointer = %d, want 4", got)
	}

	if err := f.Erase(0); err != nil {
		t.Fatal(err)
	}
	if got := f.EraseCount(0); got != 1 {
		t.Fatalf("erase count = %d, want 1", got)
	}
	if err := f.Program(0, 0, data, oob); err != nil {
		t.Fatalf("program after erase: %v", err)
	}
}

func TestTrimPersistsAndPreventsZombieResurrection(t *testing.T) {
	cfg := testConfig()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	old := payload(1)
	mustWrite(t, d, 7, old)
	oldPPA, ok := d.Lookup(7)
	if !ok {
		t.Fatal("lba 7 not mapped")
	}
	meta, ok := d.Flash().PageMeta(oldPPA.Block, oldPPA.Page)
	if !ok || meta.LBA != 7 {
		t.Fatal("old page should still carry zombie LBA in OOB")
	}

	// Put the live mapping into a checkpoint first: the recovery path must
	// then let the durable TRIM record kill the checkpointed mapping.
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := d.Trim(7, 1); err != nil {
		t.Fatal(err)
	}
	if len(d.Meta().TrimJournal()) == 0 {
		t.Fatal("TRIM was not journaled")
	}

	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Lookup(7); ok {
		t.Fatal("trimmed LBA resurrected after recovery")
	}
	got, err := d.Read(7)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, make([]byte, cfg.PageSize)) {
		t.Fatalf("trimmed read = %x, want deterministic zeroes", got)
	}
	if state := d.PageState(oldPPA); state != PageInvalid {
		t.Fatalf("old page state = %s, want invalid", state)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}

	// A checkpoint after TRIM must retain the tombstone without the journal.
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Lookup(7); ok {
		t.Fatal("trimmed LBA resurrected after checkpoint recovery")
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestSparseLargeTrimRange(t *testing.T) {
	cfg := testConfig()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, d, 7, payload(7))
	mustWrite(t, d, 1_000_000, payload(8))
	if err := d.Trim(0, 100); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Lookup(7); ok {
		t.Fatal("lba 7 should be trimmed")
	}
	mustEqual(t, d, 1_000_000, payload(8))
	if err := d.Trim(0, 2_000_000); err != nil {
		t.Fatal(err)
	}
	if len(d.Mapping()) != 0 {
		t.Fatal("large sparse trim did not release every mapping")
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if len(d.Mapping()) != 0 {
		t.Fatal("trimmed sparse mappings resurrected")
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestTornPageIsDiscardedAndWritePointerAdvances(t *testing.T) {
	cfg := testConfig()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, d, 1, payload(1))
	mustWrite(t, d, 2, payload(2))
	victim, ok := d.Lookup(2)
	if !ok {
		t.Fatal("lba 2 not mapped")
	}
	wpBefore := d.WritePointer(victim.Block)
	if err := d.Flash().InjectOOBFlip(victim.Block, victim.Page, 16); err != nil {
		t.Fatal(err)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}

	if _, ok := d.Lookup(2); ok {
		t.Fatal("torn page was accepted during recovery")
	}
	got, err := d.Read(2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, make([]byte, cfg.PageSize)) {
		t.Fatal("torn page read did not return zeroes")
	}
	mustEqual(t, d, 1, payload(1))
	if state := d.PageState(victim); state != PageTorn {
		t.Fatalf("state = %s, want torn", state)
	}
	if got := d.WritePointer(victim.Block); got != wpBefore {
		t.Fatalf("write pointer = %d, want consumed slot %d", got, wpBefore)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestTornOverwriteRollsBackOnlyToOlderCompletePage(t *testing.T) {
	d, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	old := payload(10)
	newer := payload(11)
	mustWrite(t, d, 5, old)
	mustWrite(t, d, 5, newer)
	newPPA, _ := d.Lookup(5)
	if err := d.Flash().InjectOOBFlip(newPPA.Block, newPPA.Page, 24); err != nil {
		t.Fatal(err)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	mustEqual(t, d, 5, old)
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointFallbackWithRetainedTrimRedo(t *testing.T) {
	d, err := New(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, d, 1, payload(1))
	mustWrite(t, d, 2, payload(2))
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := d.Trim(1, 1); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, d, 3, payload(3))
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	slots := d.Meta().CheckpointSlotSeqs()
	newest := -1
	if slots[0] > slots[1] {
		newest = 0
	} else {
		newest = 1
	}
	if err := d.Meta().CorruptCheckpointSlot(newest); err != nil {
		t.Fatal(err)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.Lookup(1); ok {
		t.Fatal("TRIM from journal redo lost after checkpoint fallback")
	}
	mustEqual(t, d, 2, payload(2))
	mustEqual(t, d, 3, payload(3))
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestGCMidMigrationTearLosesNoDataAndDoesNotRollBack(t *testing.T) {
	cfg := Config{
		BlockCount:         6,
		PagesPerBlock:      8,
		PageSize:           64,
		ReservedFreeBlocks: 1,
		WearLevelThreshold: 2,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[int][]byte)
	for lba := 0; lba < 8; lba++ {
		expected[lba] = payload(lba)
		mustWrite(t, d, lba, expected[lba])
	}
	// Invalidate the first half of block 0 while keeping the second half live.
	for lba := 0; lba < 4; lba++ {
		expected[lba] = payload(lba + 100)
		mustWrite(t, d, lba, expected[lba])
	}

	gcPrograms := 0
	d.SetProgramHook(func(info ProgramInfo) ProgramFault {
		if info.GC {
			gcPrograms++
			if gcPrograms == 2 {
				return ProgramFault{Tear: true, DataBytes: 17, OOBBytes: 13}
			}
		}
		return ProgramFault{}
	})
	if ok, err := d.GCOnce(); !ok || !errors.Is(err, ErrProgramTorn) {
		t.Fatalf("GCOnce = %v, %v; want progressed + torn", ok, err)
	}
	d.SetProgramHook(nil)
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}

	// Snapshot the exact pre-power-loss mapping; recovery must match it.
	before := d.Mapping()
	wpBefore := make([]int, cfg.BlockCount)
	for block := range wpBefore {
		wpBefore[block] = d.WritePointer(block)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := d.Mapping(); !mappingEqual(got, before) {
		t.Fatalf("mapping changed across recovery:\n got=%v\nwant=%v", got, before)
	}
	for block := range wpBefore {
		if got := d.WritePointer(block); got != wpBefore[block] {
			t.Fatalf("block %d write pointer = %d, want %d", block, got, wpBefore[block])
		}
	}
	for lba, want := range expected {
		mustEqual(t, d, lba, want)
	}
}

func TestGCMigrationCompletesAndSurvivesPowerLoss(t *testing.T) {
	cfg := Config{BlockCount: 6, PagesPerBlock: 8, PageSize: 64, ReservedFreeBlocks: 1}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[int][]byte)
	for lba := 0; lba < 8; lba++ {
		expected[lba] = payload(lba)
		mustWrite(t, d, lba, expected[lba])
	}
	for lba := 0; lba < 4; lba++ {
		expected[lba] = payload(lba + 50)
		mustWrite(t, d, lba, expected[lba])
	}
	countsBefore := d.EraseCounts()
	if ok, err := d.GCOnce(); !ok || err != nil {
		t.Fatalf("GCOnce = %v, %v", ok, err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	for lba, want := range expected {
		mustEqual(t, d, lba, want)
	}
	incremented := false
	for block, before := range countsBefore {
		if d.EraseCount(block) > before {
			incremented = true
		}
	}
	if !incremented {
		t.Fatal("GC did not erase any block")
	}
}

func TestGCPowerOffAfterCompletedCopiesKeepsNewestSeq(t *testing.T) {
	cfg := Config{BlockCount: 6, PagesPerBlock: 8, PageSize: 64, ReservedFreeBlocks: 1}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	expected := make(map[int][]byte)
	for lba := 0; lba < 8; lba++ {
		expected[lba] = payload(lba)
		mustWrite(t, d, lba, expected[lba])
	}
	for lba := 0; lba < 4; lba++ {
		expected[lba] = payload(lba + 80)
		mustWrite(t, d, lba, expected[lba])
	}
	gcPrograms := 0
	d.SetProgramHook(func(info ProgramInfo) ProgramFault {
		if info.GC {
			gcPrograms++
			if gcPrograms == 3 {
				return ProgramFault{PowerOff: true}
			}
		}
		return ProgramFault{}
	})
	if ok, err := d.GCOnce(); !ok || !errors.Is(err, ErrPoweredOff) {
		t.Fatalf("GCOnce = %v, %v; want progressed + powered off", ok, err)
	}
	d.SetProgramHook(nil)
	if d.Powered() {
		t.Fatal("device should be powered off")
	}
	if err := d.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	for lba, want := range expected {
		mustEqual(t, d, lba, want)
	}
}

func TestRandomWorkloadMatchesReferenceModelAcrossRecovery(t *testing.T) {
	cfg := Config{
		BlockCount:         10,
		PagesPerBlock:      8,
		PageSize:           64,
		ReservedFreeBlocks: 2,
		WearLevelThreshold: 2,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	model := make(map[int][]byte)
	rng := rand.New(rand.NewSource(42))
	for step := 0; step < 400; step++ {
		switch rng.Intn(10) {
		case 0, 1:
			start := rng.Intn(40)
			count := 1 + rng.Intn(4)
			if err := d.Trim(start, count); err != nil {
				t.Fatalf("step %d trim: %v", step, err)
			}
			for lba := start; lba < start+count; lba++ {
				delete(model, lba)
			}
		case 2:
			if err := d.Checkpoint(); err != nil {
				t.Fatalf("step %d checkpoint: %v", step, err)
			}
		default:
			lba := rng.Intn(40)
			data := payload(step)
			if err := d.Write(lba, data); err != nil {
				t.Fatalf("step %d write lba %d: %v", step, lba, err)
			}
			model[lba] = data
		}
		if step%37 == 0 {
			if err := d.PowerCycle(); err != nil {
				t.Fatalf("step %d power cycle: %v", step, err)
			}
			if err := d.Verify(); err != nil {
				t.Fatalf("step %d verify: %v", step, err)
			}
		}
	}
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := d.Mapping(); len(got) != len(model) {
		t.Fatalf("mapping size = %d, model size = %d", len(got), len(model))
	}
	for lba := 0; lba < 40; lba++ {
		want, ok := model[lba]
		if !ok {
			if _, mapped := d.Lookup(lba); mapped {
				t.Fatalf("lba %d unexpectedly mapped", lba)
			}
			continue
		}
		mustEqual(t, d, lba, want)
	}
}

func TestOpenRebuildsPersistentState(t *testing.T) {
	cfg := testConfig()
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for lba := 0; lba < 12; lba++ {
		mustWrite(t, d, lba, payload(lba))
	}
	if err := d.Trim(3, 2); err != nil {
		t.Fatal(err)
	}
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, d, 20, payload(20))
	want := d.Mapping()

	reopened, err := Open(d.Flash(), d.Meta(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Mapping(); !mappingEqual(got, want) {
		t.Fatalf("reopened mapping mismatch:\n got=%v\nwant=%v", got, want)
	}
	for _, lba := range []int{0, 1, 2, 5, 20} {
		mustEqual(t, reopened, lba, payload(lba))
	}
	if _, ok := reopened.Lookup(3); ok {
		t.Fatal("trimmed lba 3 present after reopen")
	}
	if err := reopened.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryReconstructsExactMappingBitmapsAndPointers(t *testing.T) {
	cfg := Config{
		BlockCount:         8,
		PagesPerBlock:      8,
		PageSize:           64,
		ReservedFreeBlocks: 1,
		WearLevelThreshold: 2,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for lba := 0; lba < 20; lba++ {
		mustWrite(t, d, lba, payload(lba))
	}
	if err := d.Trim(2, 3); err != nil {
		t.Fatal(err)
	}
	if err := d.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	for lba := 0; lba < 12; lba++ {
		mustWrite(t, d, lba, payload(lba+40))
	}
	if err := d.CollectGarbage(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}

	mappingBefore := d.Mapping()
	bitmapsBefore := make([][]bool, cfg.BlockCount)
	wpBefore := make([]int, cfg.BlockCount)
	for block := 0; block < cfg.BlockCount; block++ {
		bitmapsBefore[block] = d.ValidBitmap(block)
		wpBefore[block] = d.WritePointer(block)
	}
	eraseBefore := d.EraseCounts()

	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	if got := d.Mapping(); !mappingEqual(got, mappingBefore) {
		t.Fatalf("L2P mismatch:\n got=%v\nwant=%v", got, mappingBefore)
	}
	for block := 0; block < cfg.BlockCount; block++ {
		got := d.ValidBitmap(block)
		if len(got) != len(bitmapsBefore[block]) {
			t.Fatalf("block %d bitmap length %d", block, len(got))
		}
		for page := range got {
			if got[page] != bitmapsBefore[block][page] {
				t.Fatalf("block %d page %d valid=%v want %v", block, page, got[page], bitmapsBefore[block][page])
			}
		}
		if got := d.WritePointer(block); got != wpBefore[block] {
			t.Fatalf("block %d write pointer %d want %d", block, got, wpBefore[block])
		}
	}
	eraseAfter := d.EraseCounts()
	for block := range eraseBefore {
		if eraseBefore[block] != eraseAfter[block] {
			t.Fatalf("block %d erase count %d want %d", block, eraseAfter[block], eraseBefore[block])
		}
	}
}

func TestPhysicalPageStateConservation(t *testing.T) {
	cfg := Config{BlockCount: 8, PagesPerBlock: 8, PageSize: 48, ReservedFreeBlocks: 1}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for lba := 0; lba < 30; lba++ {
		mustWrite(t, d, lba%20, payload(lba))
	}
	context := 0
	d.SetProgramHook(func(info ProgramInfo) ProgramFault {
		context++
		if context == 9 {
			return ProgramFault{Tear: true}
		}
		return ProgramFault{}
	})
	_ = d.Write(99, payload(99))
	d.SetProgramHook(nil)
	if err := d.PowerCycle(); err != nil {
		t.Fatal(err)
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}

	states := map[PageState]int{}
	for block := 0; block < cfg.BlockCount; block++ {
		for page := 0; page < cfg.PagesPerBlock; page++ {
			states[d.PageState(PPA{Block: block, Page: page})]++
		}
	}
	total := 0
	for _, count := range states {
		total += count
	}
	if total != cfg.BlockCount*cfg.PagesPerBlock {
		t.Fatalf("state count %d != physical pages %d", total, cfg.BlockCount*cfg.PagesPerBlock)
	}
	if states[PageValid] != len(d.Mapping()) {
		t.Fatalf("valid pages %d != mapping %d", states[PageValid], len(d.Mapping()))
	}
}

func TestWearLevelingConvergesUnderHotColdSkew(t *testing.T) {
	cfg := Config{
		BlockCount:         10,
		PagesPerBlock:      12,
		PageSize:           32,
		ReservedFreeBlocks: 1,
		WearLevelThreshold: 2,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// One static cold block.
	for lba := 0; lba < 12; lba++ {
		mustWrite(t, d, lba, payload(lba))
	}
	// A small hot working set that is overwritten forever.
	for lba := 100; lba < 112; lba++ {
		mustWrite(t, d, lba, payload(lba))
	}
	rng := rand.New(rand.NewSource(7))
	for step := 0; step < 1500; step++ {
		lba := 100 + rng.Intn(12)
		if err := d.Write(lba, payload(step)); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
	delta := d.WearDelta()
	t.Logf("erase counts=%v delta=%d write amplification=%.2f",
		d.EraseCounts(), delta, d.Stats().WriteAmplification())
	if delta > cfg.WearLevelThreshold+1 {
		t.Fatalf("wear delta %d did not converge to threshold %d", delta, cfg.WearLevelThreshold)
	}
}

func TestGCReserveNeverDeadlocks(t *testing.T) {
	cfg := Config{
		BlockCount:         8,
		PagesPerBlock:      8,
		PageSize:           32,
		ReservedFreeBlocks: 1,
		WearLevelThreshold: 2,
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 5000; step++ {
		lba := step % 20
		if err := d.Write(lba, payload(step)); err != nil {
			t.Fatalf("step %d write lba %d: %v", step, lba, err)
		}
		if free := d.FreeBlocks(); free < cfg.ReservedFreeBlocks {
			t.Fatalf("step %d: free blocks %d fell below reserve %d", step, free, cfg.ReservedFreeBlocks)
		}
	}
	if err := d.Verify(); err != nil {
		t.Fatal(err)
	}
}

func mappingEqual(a, b map[int]PPA) bool {
	if len(a) != len(b) {
		return false
	}
	for lba, ppa := range a {
		if b[lba] != ppa {
			return false
		}
	}
	return true
}

func ExampleDevice() {
	d, err := New(Config{BlockCount: 4, PagesPerBlock: 4, PageSize: 32})
	if err != nil {
		panic(err)
	}
	_ = d.Write(0, []byte("hello"))
	value, _ := d.Read(0)
	fmt.Println(string(value))
	_ = d.Trim(0, 1)
	value, _ = d.Read(0)
	fmt.Println(len(value), value[0])
	// Output:
	// hello
	// 32 0
}
