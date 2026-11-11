package bootjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Layout constants. Each slot owns one header page plus dataPages image
// pages; the remaining pages hold the commit journal.
const (
	SlotCount   = 2
	slotHeader  = "IMGH"
	markerReady = 0x00 // committed header; an erased header reads 0xFF here
	dataPages   = 4
	slotBytes   = (1 + dataPages) * PageSize
	imageCap    = dataPages * PageSize
)

// Errors returned by the device state machine.
var (
	// ErrRollbackVersion refuses an update older than the confirmed floor.
	ErrRollbackVersion = errors.New("bootjournal: image version below anti-rollback floor")
	// ErrBadImage refuses an image whose stored hash does not match its bytes.
	ErrBadImage = errors.New("bootjournal: image verification failed")
	// ErrNoBootableImage means neither slot holds a verified image.
	ErrNoBootableImage = errors.New("bootjournal: no verifiable image available")
	// ErrNotProvisioned means the factory image has not been installed yet.
	ErrNotProvisioned = errors.New("bootjournal: device not provisioned")
)

// Image is a versioned firmware payload. The device only trusts an image
// whose SHA-256 matches its own contents.
type Image struct {
	Version uint32
	Data    []byte
}

func (img Image) hash() [sha256.Size]byte { return sha256.Sum256(img.Data) }

// Device is a dual-slot firmware device backed by a FaultyFlash.
type Device struct {
	flash *FaultyFlash
	log   *journal
}

// Open replays the commit log and returns the recovered device.
func Open(flash *FaultyFlash) (*Device, error) {
	used := SlotCount * slotBytes
	journalPages := (flash.Size() - used) / PageSize
	log, err := openJournal(flash, used, journalPages)
	if err != nil {
		return nil, err
	}
	return &Device{flash: flash, log: log}, nil
}

func slotBase(slot int) int { return slot * slotBytes }

func imageBase(slot int) int { return slotBase(slot) + PageSize }

// headerOK reports whether the slot header is committed and consistent.
func (d *Device) headerOK(slot int) (bool, uint32) {
	buf := make([]byte, PageSize)
	if err := d.flash.Read(slotBase(slot), buf); err != nil {
		return false, 0
	}
	if string(buf[0:4]) != slotHeader || buf[4] != markerReady {
		return false, 0
	}
	version := binary.BigEndian.Uint32(buf[8:12])
	var stored [sha256.Size]byte
	copy(stored[:], buf[16:16+sha256.Size])
	img := d.readImage(slot)
	if img == nil || sha256.Sum256(img) != stored {
		return false, 0
	}
	return true, version
}

// readImage returns the image bytes stored in a slot, or nil when the slot is
// blank/torn. The length lives in the header.
func (d *Device) readImage(slot int) []byte {
	buf := make([]byte, PageSize)
	if err := d.flash.Read(slotBase(slot), buf); err != nil {
		return nil
	}
	length := int(binary.BigEndian.Uint32(buf[12:16]))
	if length <= 0 || length > imageCap {
		return nil
	}
	out := make([]byte, length)
	if err := d.flash.Read(imageBase(slot), out); err != nil {
		return nil
	}
	return out
}

// VerifiedImage returns the verified image occupying slot, or ok=false.
func (d *Device) VerifiedImage(slot int) (Image, bool) {
	if slot < 0 || slot >= SlotCount {
		return Image{}, false
	}
	ok, version := d.headerOK(slot)
	if !ok {
		return Image{}, false
	}
	return Image{Version: version, Data: d.readImage(slot)}, true
}

// ActiveSlot returns the slot selected for booting, -1 before provisioning.
func (d *Device) ActiveSlot() int { return d.log.state.active }

// MinVersion returns the anti-rollback floor.
func (d *Device) MinVersion() uint32 { return d.log.state.minVersion }

// UnconfirmedBoots reports how many times the active image booted without a
// matching Confirm since it became active.
func (d *Device) UnconfirmedBoots() int { return d.log.state.unconfirmed }

// activeConfirmed reports whether slot currently has a zero unconfirmed-boot
// count, i.e. its last boot was confirmed.
func (d *Device) activeConfirmed(slot int) bool {
	return d.log.state.active == slot && d.log.state.unconfirmed == 0
}

// Provision installs the immutable factory image into slot 0 and makes it the
// active slot. It must run once on a blank device.
func (d *Device) Provision(img Image) error {
	if d.log.state.active >= 0 {
		// Idempotent after a power loss mid-provision: if the factory image is
		// already present and verified in slot 0, the earlier attempt won.
		if existing, ok := d.VerifiedImage(0); ok && existing.Version == img.Version && bytes.Equal(existing.Data, img.Data) {
			return nil
		}
		return errors.New("bootjournal: device already provisioned")
	}
	if len(img.Data) == 0 || len(img.Data) > imageCap {
		return errors.New("bootjournal: factory image out of range")
	}
	if err := d.writeSlot(0, img); err != nil {
		return err
	}
	initial := logState{active: 0, minVersion: img.Version, pendingSlot: -1}
	payload := snapshotPayload(initial)
	if err := d.log.append(recSnapshot, payload); err != nil {
		return err
	}
	if err := d.log.append(recActivate, slotPayload(0)); err != nil {
		return err
	}
	return nil
}

// writeSlot erases the destination slot, programs the image word by word, and
// commits the verified header last. The active slot is never a legal target:
// callers only ever update the inactive slot, so one good image always
// survives an interrupted write.
func (d *Device) writeSlot(slot int, img Image) error {
	if len(img.Data) > imageCap {
		return errors.New("bootjournal: image larger than slot")
	}
	base := slotBase(slot)
	if err := d.flash.EraseRange(base, slotBytes); err != nil {
		return err
	}
	if err := d.flash.Program(imageBase(slot), img.Data); err != nil {
		return err
	}
	// Verify before committing the header; nothing marks this slot bootable
	// until the hash matches.
	got := make([]byte, len(img.Data))
	if err := d.flash.Read(imageBase(slot), got); err != nil {
		return err
	}
	if sha256.Sum256(got) != img.hash() {
		return ErrBadImage
	}
	header := make([]byte, PageSize)
	for i := range header {
		header[i] = 0xFF
	}
	copy(header[0:4], slotHeader)
	header[4] = markerReady
	binary.BigEndian.PutUint32(header[8:12], img.Version)
	binary.BigEndian.PutUint32(header[12:16], uint32(len(img.Data)))
	sum := img.hash()
	copy(header[16:16+sha256.Size], sum[:])
	// Header data first, then the ready marker word as the 1 -> 0 commit.
	if err := d.flash.Program(base, header); err != nil {
		return err
	}
	marker := []byte{markerReady, 0xFF, 0xFF, 0xFF}
	if err := d.flash.Program(base+4, marker); err != nil {
		return err
	}
	// Re-read and verify through the public path before reporting success.
	if ok, _ := d.headerOK(slot); !ok {
		return ErrBadImage
	}
	return nil
}

// Update stages img into the inactive slot. The active slot is never touched
// and the boot selection cannot change until the new image has been fully
// written and verified.
func (d *Device) Update(img Image) error {
	if d.log.state.active < 0 {
		return ErrNotProvisioned
	}
	if len(img.Data) == 0 || len(img.Data) > imageCap {
		return errors.New("bootjournal: image out of range")
	}
	// Anti-rollback: the floor only ever advances, and only after confirm.
	if img.Version < d.log.state.minVersion {
		return ErrRollbackVersion
	}
	target := 1 - d.log.state.active
	if err := d.writeSlot(target, img); err != nil {
		return err
	}
	payload := make([]byte, 52)
	payload[payloadSlot] = byte(target)
	binary.BigEndian.PutUint32(payload[payloadVersion:payloadVersion+4], img.Version)
	if err := d.log.append(recPrepared, payload); err != nil {
		return err
	}
	return nil
}

// Boot selects the slot to run after reset. A prepared update switches the
// active slot only now (its image was already verified). Every unconfirmed
// boot counts; two consecutive unconfirmed boots roll back to the other slot.
func (d *Device) Boot() (int, error) {
	if d.log.state.active < 0 {
		return -1, ErrNotProvisioned
	}
	if d.log.state.pendingSlot >= 0 {
		slot := d.log.state.pendingSlot
		ok, _ := d.headerOK(slot)
		if !ok {
			return -1, ErrNoBootableImage
		}
		if err := d.log.append(recActivate, slotPayload(slot)); err != nil {
			return -1, err
		}
		return slot, nil
	}

	slot := d.log.state.active
	ok, _ := d.headerOK(slot)
	if !ok {
		return -1, ErrNoBootableImage
	}
	if err := d.log.append(recBoot, slotPayload(slot)); err != nil {
		return -1, err
	}
	if d.log.state.unconfirmed < 2 {
		return slot, nil
	}

	// Two consecutive unconfirmed boots: roll back to the other slot, which
	// must itself hold a verifiable image.
	other := 1 - slot
	ok, _ = d.headerOK(other)
	if !ok {
		return -1, ErrNoBootableImage
	}
	if err := d.log.append(recRollback, slotPayload(other)); err != nil {
		return -1, err
	}
	return other, nil
}

// Confirm marks the running image good. This is the sole event that advances
// the anti-rollback floor and clears the consecutive-failure counter.
func (d *Device) Confirm(slot int) error {
	if d.log.state.active < 0 {
		return ErrNotProvisioned
	}
	if slot != d.log.state.active {
		return errors.New("bootjournal: only the active slot can be confirmed")
	}
	ok, version := d.headerOK(slot)
	if !ok {
		return ErrNoBootableImage
	}
	if err := d.log.append(recConfirm, confirmPayload(slot, version)); err != nil {
		return err
	}
	return nil
}

func confirmPayload(slot int, version uint32) []byte {
	payload := make([]byte, 52)
	payload[payloadSlot] = byte(slot)
	binary.BigEndian.PutUint32(payload[payloadVersion:payloadVersion+4], version)
	return payload
}

func slotPayload(slot int) []byte {
	payload := make([]byte, 52)
	payload[payloadSlot] = byte(slot)
	return payload
}
