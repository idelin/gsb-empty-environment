// Package zdevice simulates a zoned, append-only block device with two
// atomically swappable root table slots.
//
// The simulation implements a real persistent/volatile boundary: every
// mutation changes only in-memory state until Flush persists an image to
// disk. PowerOff discards all unflushed state, modeling sudden power loss.
package zdevice

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"path/filepath"

	"prescreen.local/ps-7e522fc03b/zplanner"
)

const (
	// Zones is the fixed number of zones.
	Zones = zplanner.MaxZones
	// PagesPerZone is the fixed number of append-only pages per zone.
	PagesPerZone = zplanner.PagesPerZone
)

var (
	// ErrPoweredOff is returned when a powered-on operation is attempted
	// while the device has no volatile state.
	ErrPoweredOff = errors.New("zdevice: device is powered off")
	// ErrZoneFull is returned when appending to a full zone.
	ErrZoneFull = errors.New("zdevice: zone is full")
	// ErrInvalidPhys is returned when reading a page that was never written
	// or has been reset.
	ErrInvalidPhys = errors.New("zdevice: invalid physical address")
	// ErrPinned is returned when resetting a zone referenced by a bootable
	// slot or by an unflushed/staged page.
	ErrPinned = errors.New("zdevice: zone is pinned by a bootable slot")
	// ErrPublishPending is returned when a second publish is attempted
	// before the previous one was acknowledged.
	ErrPublishPending = errors.New("zdevice: previous publish is not acknowledged")
	// ErrTornSlot is returned when a slot image fails its CRC check.
	ErrTornSlot = errors.New("zdevice: torn root table slot")
)

type zoneState struct {
	Pages [PagesPerZone][]byte
	WP    int
}

type slotRecord struct {
	Generation int
	Data       []byte
	CRC        uint64
}

type persistent struct {
	Formatted bool
	Zones     [Zones]zoneState
	Slots     [2]slotRecord
	SlotValid [2]bool
	Current   int
	// Pending is true after an atomic swap and before an Ack: both slots are
	// bootable and firmware may start from either one.
	Pending bool
}

// Device is a resettable simulation backed by one image file.
type Device struct {
	imagePath string
	vol       *persistent
}

// NewDevice opens (or creates the handle for) a device whose durable image
// lives in dir.
func NewDevice(dir string) *Device {
	return &Device{imagePath: filepath.Join(dir, "zdevice.img")}
}

// Dir returns the directory holding the durable image.
func (d *Device) Dir() string { return filepath.Dir(d.imagePath) }

// Format initializes empty durable state, including an empty generation-0
// boot slot, and powers the device on.
func (d *Device) Format() error {
	if _, err := os.Stat(d.imagePath); err == nil {
		return fmt.Errorf("zdevice: image already exists")
	}
	d.vol = &persistent{Formatted: true, Current: 0}
	empty := &zplanner.Table{
		LastOp: -1,
		Loc:    map[zplanner.CID]zplanner.Phys{},
		Roots:  map[string]zplanner.CID{},
		Snaps:  map[string]map[string]zplanner.CID{},
	}
	data, err := encodeTable(empty)
	if err != nil {
		return err
	}
	d.vol.Slots[0] = slotRecord{
		Generation: 0,
		Data:       data,
		CRC:        crc64.Checksum(data, crcTable),
	}
	d.vol.SlotValid[0] = true
	return d.Flush()
}

// PoweredOn reports whether volatile state exists.
func (d *Device) PoweredOn() bool { return d.vol != nil }

// PowerOn loads the durable image. When a publish is unacknowledged both
// slots are valid; bootSlot selects which of the two allowed slots firmware
// starts from. Otherwise the sole valid slot is used.
func (d *Device) PowerOn(bootSlot int) (*zplanner.Table, int, error) {
	raw, err := os.ReadFile(d.imagePath)
	if err != nil {
		return nil, 0, err
	}
	st := &persistent{}
	if err := gob.NewDecoder(bytes.NewReader(raw)).Decode(st); err != nil {
		return nil, 0, fmt.Errorf("zdevice: corrupt image: %w", err)
	}
	d.vol = st

	choice := st.Current
	if st.Pending {
		if bootSlot != 0 && bootSlot != 1 {
			return nil, 0, fmt.Errorf("zdevice: boot slot must be 0 or 1")
		}
		if !st.SlotValid[bootSlot] {
			return nil, 0, fmt.Errorf("%w: slot %d unreadable", ErrTornSlot, bootSlot)
		}
		choice = bootSlot
	} else if !st.SlotValid[choice] {
		return nil, 0, fmt.Errorf("%w: current slot %d unreadable", ErrTornSlot, choice)
	}
	tbl, err := decodeTable(st.Slots[choice])
	if err != nil {
		return nil, 0, err
	}
	return tbl, choice, nil
}

// PowerOff drops all volatile state, including anything not yet flushed.
func (d *Device) PowerOff() { d.vol = nil }

// Flush atomically persists the volatile state.
func (d *Device) Flush() error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(d.vol); err != nil {
		return err
	}
	tmp := d.imagePath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.imagePath)
}

// Append writes data at the tail of zone z. The write is volatile until
// Flush; a power loss before Flush leaves the write pointer unchanged.
func (d *Device) Append(z zplanner.ZoneID, data []byte) (zplanner.Phys, error) {
	if d.vol == nil {
		return zplanner.Phys{}, ErrPoweredOff
	}
	if int(z) >= Zones {
		return zplanner.Phys{}, fmt.Errorf("zdevice: bad zone %d", z)
	}
	zs := &d.vol.Zones[z]
	if zs.WP >= PagesPerZone {
		return zplanner.Phys{}, ErrZoneFull
	}
	payload := append([]byte(nil), data...)
	phys := zplanner.Phys{Zone: z, Off: uint8(zs.WP)}
	zs.Pages[zs.WP] = payload
	zs.WP++
	return phys, nil
}

// Read returns the page contents at phys.
func (d *Device) Read(phys zplanner.Phys) ([]byte, error) {
	if d.vol == nil {
		return nil, ErrPoweredOff
	}
	if int(phys.Zone) >= Zones || int(phys.Off) >= d.vol.Zones[phys.Zone].WP {
		return nil, ErrInvalidPhys
	}
	return append([]byte(nil), d.vol.Zones[phys.Zone].Pages[phys.Off]...), nil
}

// Fill reports the current append offset of every zone.
func (d *Device) Fill() ([Zones]int, error) {
	if d.vol == nil {
		return [Zones]int{}, ErrPoweredOff
	}
	var out [Zones]int
	for z := range d.vol.Zones {
		out[z] = d.vol.Zones[z].WP
	}
	return out, nil
}

// WriteSlot stages a new root table into the inactive slot. The device
// refuses while an earlier publish is still unacknowledged.
func (d *Device) WriteSlot(tbl *zplanner.Table) error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	if d.vol.Pending {
		return ErrPublishPending
	}
	data, err := encodeTable(tbl)
	if err != nil {
		return err
	}
	target := 1 - d.vol.Current
	d.vol.Slots[target] = slotRecord{
		Generation: tbl.Generation,
		Data:       data,
		CRC:        crc64.Checksum(data, crcTable),
	}
	d.vol.SlotValid[target] = true
	return nil
}

// Swap atomically makes the staged slot current and marks the previous slot
// as retained-but-unacknowledged.
func (d *Device) Swap() error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	target := 1 - d.vol.Current
	if !d.vol.SlotValid[target] {
		return fmt.Errorf("zdevice: inactive slot %d is empty", target)
	}
	if d.vol.Pending {
		return ErrPublishPending
	}
	d.vol.Current = target
	d.vol.Pending = true
	return nil
}

// Ack acknowledges the current publish: firmware drops the previous slot.
func (d *Device) Ack() error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	if !d.vol.Pending {
		return fmt.Errorf("zdevice: no pending publish to acknowledge")
	}
	old := 1 - d.vol.Current
	d.vol.SlotValid[old] = false
	d.vol.Slots[old] = slotRecord{}
	d.vol.Pending = false
	return d.Flush()
}

// Pending reports whether an unacknowledged publish retains both slots.
func (d *Device) Pending() (bool, error) {
	if d.vol == nil {
		return false, ErrPoweredOff
	}
	return d.vol.Pending, nil
}

// CurrentSlot returns the physical index of the current slot.
func (d *Device) CurrentSlot() (int, error) {
	if d.vol == nil {
		return 0, ErrPoweredOff
	}
	return d.vol.Current, nil
}

// ResetZone erases zone z. Both bootable slots' physical references pin the
// zone; the call fails while any such page lives there. The reset is
// volatile until Flush.
func (d *Device) ResetZone(z zplanner.ZoneID) error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	if int(z) >= Zones {
		return fmt.Errorf("zdevice: bad zone %d", z)
	}
	pinned, err := d.pinnedZones()
	if err != nil {
		return err
	}
	if pinned[z] {
		return fmt.Errorf("%w: zone %d", ErrPinned, z)
	}
	d.vol.Zones[z] = zoneState{}
	return nil
}

// CorruptSlot flips a byte in the named slot's CRC-protected image, used to
// exercise torn-slot detection after power loss.
func (d *Device) CorruptSlot(slot int) error {
	if d.vol == nil {
		return ErrPoweredOff
	}
	if slot < 0 || slot > 1 || !d.vol.SlotValid[slot] {
		return fmt.Errorf("zdevice: slot %d not valid", slot)
	}
	rec := d.vol.Slots[slot]
	if len(rec.Data) == 0 {
		return fmt.Errorf("zdevice: empty slot %d", slot)
	}
	rec.Data[0] ^= 0xFF
	rec.CRC = crc64.Checksum(rec.Data, crcTable) ^ 1
	d.vol.Slots[slot] = rec
	return nil
}

func (d *Device) pinnedZones() (map[zplanner.ZoneID]bool, error) {
	out := map[zplanner.ZoneID]bool{}
	for idx, valid := range d.vol.SlotValid {
		if !valid {
			continue
		}
		tbl, err := decodeTable(d.vol.Slots[idx])
		if err != nil {
			return nil, err
		}
		for _, phys := range tbl.Loc {
			out[phys.Zone] = true
		}
	}
	return out, nil
}

var crcTable = crc64.MakeTable(crc64.ECMA)

func encodeTable(tbl *zplanner.Table) ([]byte, error) {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(tbl); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func decodeTable(rec slotRecord) (*zplanner.Table, error) {
	if crc64.Checksum(rec.Data, crcTable) != rec.CRC {
		return nil, fmt.Errorf("%w: generation %d", ErrTornSlot, rec.Generation)
	}
	tbl := &zplanner.Table{}
	if err := gob.NewDecoder(bytes.NewReader(rec.Data)).Decode(tbl); err != nil {
		return nil, fmt.Errorf("%w: decode failure", ErrTornSlot)
	}
	return tbl, nil
}
