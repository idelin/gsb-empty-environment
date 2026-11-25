package upgradeplan

import (
	"sort"
	"strconv"
	"strings"
)

// Fleet is one concrete, unordered device group state.
type Fleet []DeviceState

// CanonicalFleet normalizes signer order and returns devices sorted by a
// collision-resistant textual key. Device input order is therefore irrelevant.
func CanonicalFleet(in []DeviceState) Fleet {
	out := make(Fleet, len(in))
	keys := make([]string, len(in))
	for i, device := range in {
		device.Signers = device.Signers.Normalize()
		if device.HasData && !device.InitialData {
			device.InitialData = true
		}
		out[i] = device
		keys[i] = deviceKeyNormalized(device)
	}

	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return keys[order[i]] < keys[order[j]]
	})

	sorted := make(Fleet, len(out))
	for target, source := range order {
		sorted[target] = out[source]
	}
	return sorted
}

// DeviceKey returns the canonical textual representation of one device.
func DeviceKey(d DeviceState) string {
	d.Signers = d.Signers.Normalize()
	return deviceKeyNormalized(d)
}

func deviceKeyNormalized(d DeviceState) string {
	var b strings.Builder
	b.WriteString("D[")
	b.WriteString("pkg=")
	b.WriteString(strconv.Quote(d.Package))
	b.WriteString(";sdk=")
	b.WriteString(strconv.Itoa(d.SDK))
	b.WriteString(";range=")
	b.WriteString(strconv.Itoa(d.SDKRange.MinSDK))
	b.WriteByte(':')
	b.WriteString(strconv.Itoa(d.SDKRange.MaxSDK))
	b.WriteString(";has=")
	b.WriteString(strconv.FormatBool(d.HasData))
	b.WriteString(";initial=")
	b.WriteString(strconv.FormatBool(d.InitialData))
	b.WriteString(";lost=")
	b.WriteString(strconv.FormatBool(d.DataLost))
	b.WriteString(";signers=")
	for i, signer := range d.Signers {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(signer.canonical())
	}
	b.WriteString("]")
	return b.String()
}

// Key returns the canonical fleet key.
func (f Fleet) Key() string {
	canonical := CanonicalFleet(f)
	parts := make([]string, 0, len(canonical))
	for _, device := range canonical {
		parts = append(parts, DeviceKey(device))
	}
	return "F{" + strings.Join(parts, "|") + "}"
}

// CanonicalFleetKey is the exported convenience form of Fleet.Key.
func CanonicalFleetKey(devices []DeviceState) string {
	return Fleet(devices).Key()
}

// Belief is the set of concrete fleets the planner must handle after the same
// sequence of count observations.
type Belief struct {
	Worlds []Fleet
	key    string
}

// NewBelief canonicalizes, deduplicates, and orders concrete fleets.
func NewBelief(worlds ...[]DeviceState) *Belief {
	seen := make(map[string]Fleet)
	keys := make([]string, 0, len(worlds))
	for _, world := range worlds {
		fleet := CanonicalFleet(world)
		key := fleet.Key()
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
		seen[key] = fleet
	}
	sort.Strings(keys)
	out := &Belief{Worlds: make([]Fleet, 0, len(keys))}
	var b strings.Builder
	b.WriteString("B{")
	for i, key := range keys {
		if i > 0 {
			b.WriteString("||")
		}
		b.WriteString(key)
		out.Worlds = append(out.Worlds, seen[key])
	}
	b.WriteString("}")
	out.key = b.String()
	return out
}

// Key returns the canonical belief key. Equal keys mean indistinguishable
// planning states regardless of input ordering.
func (b *Belief) Key() string {
	if b == nil {
		return "B{}"
	}
	if b.key == "" {
		nb := NewBelief(b.WorldsAsSlices()...)
		*b = *nb
	}
	return b.key
}

// WorldsAsSlices converts fleets for variadic/exported callers.
func (b *Belief) WorldsAsSlices() [][]DeviceState {
	if b == nil {
		return nil
	}
	out := make([][]DeviceState, len(b.Worlds))
	for i, world := range b.Worlds {
		out[i] = []DeviceState(world)
	}
	return out
}

// CanonicalBeliefKey returns an order-independent key for all fleets.
func CanonicalBeliefKey(worlds [][]DeviceState) string {
	return NewBelief(worlds...).Key()
}

func beliefFromFleets(worlds ...Fleet) *Belief {
	slices := make([][]DeviceState, len(worlds))
	for i, fleet := range worlds {
		slices[i] = []DeviceState(fleet)
	}
	return NewBelief(slices...)
}
