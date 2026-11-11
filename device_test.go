package bootjournal

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func testImage(version uint32, size int) Image {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(int(version)*31 + i*7)
	}
	return Image{Version: version, Data: data}
}

func newTestDevice(t *testing.T) (*Device, *FaultyFlash) {
	t.Helper()
	// 2 slots * 5 pages + 4 journal pages = 14 pages.
	flash := NewFlash(14)
	dev, err := Open(flash)
	if err != nil {
		t.Fatalf("open blank device: %v", err)
	}
	return dev, flash
}

func provisionedDevice(t *testing.T) *Device {
	t.Helper()
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatalf("provision: %v", err)
	}
	return dev
}

func TestProvisionAndConfirm(t *testing.T) {
	dev, _ := newTestDevice(t)
	if got := dev.ActiveSlot(); got != -1 {
		t.Fatalf("blank active slot = %d, want -1", got)
	}
	img := testImage(1, 100)
	if err := dev.Provision(img); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if dev.ActiveSlot() != 0 {
		t.Fatalf("active slot = %d, want 0", dev.ActiveSlot())
	}
	got, ok := dev.VerifiedImage(0)
	if !ok || got.Version != 1 || !bytes.Equal(got.Data, img.Data) {
		t.Fatalf("slot 0 image mismatch: %+v ok=%v", got, ok)
	}
	// Re-provisioning the identical factory image is idempotent (power-loss
	// recovery), while a different image is refused.
	if err := dev.Provision(img); err != nil {
		t.Fatalf("idempotent re-provision: %v", err)
	}
	if err := dev.Provision(testImage(9, 40)); err == nil {
		t.Fatal("provision of a different image must fail")
	}
}

func TestUpdateSwitchesSlotOnlyAfterBoot(t *testing.T) {
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Update(testImage(2, 300)); err != nil {
		t.Fatalf("update: %v", err)
	}
	// The boot slot must not change before a reboot picks the staged image.
	if dev.ActiveSlot() != 0 {
		t.Fatalf("active slot = %d, want 0 until next boot", dev.ActiveSlot())
	}
	slot, err := dev.Boot()
	if err != nil || slot != 1 {
		t.Fatalf("boot = %d, %v; want slot 1", slot, err)
	}
	if dev.ActiveSlot() != 1 {
		t.Fatalf("active slot = %d, want 1", dev.ActiveSlot())
	}
	// Old slot remains a verified fallback.
	if _, ok := dev.VerifiedImage(0); !ok {
		t.Fatal("rollback slot 0 must stay verifiable")
	}
}

func TestTwoUnconfirmedBootsRollBack(t *testing.T) {
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Update(testImage(2, 300)); err != nil {
		t.Fatal(err)
	}
	first, err := dev.Boot()
	if err != nil || first != 1 {
		t.Fatalf("first boot = %d, %v", first, err)
	}
	if dev.UnconfirmedBoots() != 0 {
		t.Fatalf("activation boot must not count as unconfirmed: %d", dev.UnconfirmedBoots())
	}
	second, err := dev.Boot()
	if err != nil || second != 1 {
		t.Fatalf("second boot = %d, %v; same slot before rollback", second, err)
	}
	// The boot that reaches two consecutive unconfirmed attempts rolls back
	// within the same call.
	third, err := dev.Boot()
	if err != nil || third != 0 {
		t.Fatalf("third boot = %d, %v; want rollback to slot 0", third, err)
	}
	if dev.ActiveSlot() != 0 || dev.UnconfirmedBoots() != 0 {
		t.Fatalf("state after rollback: slot=%d unconfirmed=%d", dev.ActiveSlot(), dev.UnconfirmedBoots())
	}
}

func TestConfirmResetsCounterAndAdvancesFloor(t *testing.T) {
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Boot(); err != nil {
		t.Fatal(err)
	}
	// Confirming the factory image raises the floor to its version.
	if err := dev.Confirm(0); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if dev.MinVersion() != 1 || dev.UnconfirmedBoots() != 0 {
		t.Fatalf("floor=%d unconfirmed=%d after confirm", dev.MinVersion(), dev.UnconfirmedBoots())
	}
	// A newer image can be staged...
	if err := dev.Update(testImage(2, 300)); err != nil {
		t.Fatal(err)
	}
	// ...but the floor must not move before the new image is confirmed.
	if dev.MinVersion() != 1 {
		t.Fatalf("floor advanced before confirm: %d", dev.MinVersion())
	}
	if _, err := dev.Boot(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Confirm(1); err != nil {
		t.Fatalf("confirm new image: %v", err)
	}
	if dev.MinVersion() != 2 {
		t.Fatalf("floor = %d, want 2 after confirm", dev.MinVersion())
	}
	// Confirming a non-active slot is rejected.
	if err := dev.Confirm(0); err == nil {
		t.Fatal("confirm of inactive slot must fail")
	}
}

func TestAntiRollbackRejectsOldImage(t *testing.T) {
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(3, 100)); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Boot(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Confirm(0); err != nil {
		t.Fatal(err)
	}
	err := dev.Update(testImage(2, 100))
	if !errors.Is(err, ErrRollbackVersion) {
		t.Fatalf("old update err = %v, want ErrRollbackVersion", err)
	}
}

func TestFlashBitConstraints(t *testing.T) {
	flash := NewFlash(1)
	if err := flash.Program(0, []byte{0x0F, 0xF0}); err != nil {
		t.Fatalf("first program: %v", err)
	}
	// Programming more zero bits is legal.
	if err := flash.Program(0, []byte{0x00, 0x00}); err != nil {
		t.Fatalf("program additional zero bits: %v", err)
	}
	// Setting a bit without erase is illegal.
	if err := flash.Program(0, []byte{0x01, 0x00}); !errors.Is(err, ErrInvalidProgram) {
		t.Fatalf("zero-to-one program err = %v, want ErrInvalidProgram", err)
	}
	if err := flash.ErasePage(0); err != nil {
		t.Fatalf("erase: %v", err)
	}
	var got [2]byte
	if err := flash.Read(0, got[:]); err != nil {
		t.Fatal(err)
	}
	if got != [2]byte{0xFF, 0xFF} {
		t.Fatalf("after erase = %x, want ffff", got)
	}
}

func TestJournalCompactionSurvivesLifecycle(t *testing.T) {
	dev, flash := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatal(err)
	}
	// Each update cycle appends several records; 30 cycles far exceed the
	// 16-record journal window and force repeated snapshot compaction.
	for v := uint32(2); v <= 30; v++ {
		img := testImage(v, 50+(int(v)*7)%(imageCap-50))
		if err := dev.Update(img); err != nil {
			t.Fatalf("update v%d: %v", v, err)
		}
		slot, err := dev.Boot()
		if err != nil {
			t.Fatalf("boot v%d: %v", v, err)
		}
		if err := dev.Confirm(slot); err != nil {
			t.Fatalf("confirm v%d: %v", v, err)
		}
	}
	// Reopening replays the compacted log and must reconstruct the state.
	reopened, err := Open(flash)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.ActiveSlot() != dev.ActiveSlot() {
		t.Fatalf("active slot after reopen = %d, want %d", reopened.ActiveSlot(), dev.ActiveSlot())
	}
	if reopened.MinVersion() != 30 {
		t.Fatalf("floor after reopen = %d, want 30", reopened.MinVersion())
	}
	if _, ok := reopened.VerifiedImage(reopened.ActiveSlot()); !ok {
		t.Fatal("active image not verifiable after compaction")
	}
	// Anti-rollback still enforced post-compaction.
	if err := reopened.Update(testImage(5, 10)); !errors.Is(err, ErrRollbackVersion) {
		t.Fatalf("post-compaction rollback err = %v", err)
	}
}

func TestUnconfirmedStateReplaysAcrossReset(t *testing.T) {
	dev, flash := newTestDevice(t)
	if err := dev.Provision(testImage(1, 100)); err != nil {
		t.Fatal(err)
	}
	if err := dev.Update(testImage(2, 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Boot(); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Boot(); err != nil {
		t.Fatal(err)
	}
	after, err := Open(flash)
	if err != nil {
		t.Fatal(err)
	}
	if after.UnconfirmedBoots() != 1 {
		t.Fatalf("unconfirmed after reset = %d, want 1", after.UnconfirmedBoots())
	}
	// One more unconfirmed boot after reset reaches two and rolls back.
	slot, err := after.Boot()
	if err != nil || slot != 0 {
		t.Fatalf("rollback after reset = %d, %v", slot, err)
	}
}

func TestFlashFaultTrip(t *testing.T) {
	flash := NewFlash(1)
	flash.ArmFault()
	if err := flash.Program(0, []byte{0x00}); !errors.Is(err, ErrPowerLoss) {
		t.Fatalf("armed fault err = %v, want ErrPowerLoss", err)
	}
	if !flash.Fault() {
		t.Fatal("fault flag not latched")
	}
	// After the one-shot trip, subsequent operations behave normally.
	if err := flash.ErasePage(0); err != nil {
		t.Fatalf("erase after fault: %v", err)
	}
}

func TestSlotCapacityEnforced(t *testing.T) {
	dev, _ := newTestDevice(t)
	if err := dev.Provision(testImage(1, imageCap+1)); err == nil {
		t.Fatal("oversized image must be rejected")
	}
}

func ExampleDevice_lifecycle() {
	dev, _ := Open(NewFlash(14))
	_ = dev.Provision(Image{Version: 1, Data: []byte("factory")})
	_ = dev.Update(Image{Version: 2, Data: []byte("updated")})
	slot, _ := dev.Boot()
	_ = dev.Confirm(slot)
	fmt.Println("active slot", dev.ActiveSlot(), "floor", dev.MinVersion())
	// Output: active slot 1 floor 2
}
