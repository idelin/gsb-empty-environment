package main

import (
	"fmt"
	"strconv"
	"strings"
)

type Slot uint8

const (
	A Slot = iota
	B
	None
)

type Version uint8

const (
	Empty Version = 0
	V1    Version = 1
	V2    Version = 2

	MaxVersion Version = V2
)

type Kind uint8

const (
	Erase Kind = iota
	Write
	SetBoot
	Boot
	Confirm
	FuseIncrement
	PowerLoss
)

type Persistent struct {
	Image     [2]Version
	BootSlot  Slot
	Confirmed Version
	Fuse      Version
}

type Volatile struct {
	RunningSlot    Slot
	RunningVersion Version
}

type State struct {
	Persistent Persistent
	Volatile   Volatile
}

type Event struct {
	Kind    Kind
	Slot    Slot
	Version Version
}

func InitialState() State {
	return State{
		Persistent: Persistent{
			Image:     [2]Version{V1, Empty},
			BootSlot:  A,
			Confirmed: V1,
			Fuse:      V1,
		},
		Volatile: Volatile{
			RunningSlot:    None,
			RunningVersion: Empty,
		},
	}
}

func PossibleEvents() []Event {
	events := make([]Event, 0, 13)
	for slot := A; slot <= B; slot++ {
		events = append(events, Event{Kind: Erase, Slot: slot})
		events = append(events, Event{Kind: SetBoot, Slot: slot})
		for version := V1; version <= MaxVersion; version++ {
			events = append(events, Event{Kind: Write, Slot: slot, Version: version})
		}
	}
	events = append(events,
		Event{Kind: Boot},
		Event{Kind: Confirm},
		Event{Kind: FuseIncrement},
		Event{Kind: PowerLoss},
	)
	return events
}

func (s State) Apply(event Event) (State, bool) {
	next := s
	persistent := &next.Persistent
	volatile := &next.Volatile

	switch event.Kind {
	case Erase:
		if !s.isOff() || event.Slot > B || event.Slot == persistent.BootSlot || persistent.Image[event.Slot] == Empty {
			return State{}, false
		}
		persistent.Image[event.Slot] = Empty

	case Write:
		if !s.isOff() || event.Slot > B || event.Slot == persistent.BootSlot ||
			persistent.Image[event.Slot] != Empty || event.Version < V1 || event.Version > MaxVersion {
			return State{}, false
		}
		persistent.Image[event.Slot] = event.Version

	case SetBoot:
		if !s.isOff() || event.Slot > B || event.Slot == persistent.BootSlot ||
			persistent.Image[event.Slot] == Empty {
			return State{}, false
		}
		persistent.BootSlot = event.Slot

	case Boot:
		if !s.isOff() {
			return State{}, false
		}
		selected := persistent.BootSlot
		version := persistent.Image[selected]
		if version == Empty || version < persistent.Fuse {
			return State{}, false
		}
		volatile.RunningSlot = selected
		volatile.RunningVersion = version

	case Confirm:
		if s.isOff() || volatile.RunningVersion <= persistent.Confirmed {
			return State{}, false
		}
		persistent.Confirmed = volatile.RunningVersion
		persistent.BootSlot = volatile.RunningSlot

	case FuseIncrement:
		if s.isOff() || persistent.Fuse >= volatile.RunningVersion {
			return State{}, false
		}
		persistent.Fuse++

	case PowerLoss:
		if s.isOff() {
			return State{}, false
		}
		next.resetVolatile()

	default:
		return State{}, false
	}

	return next, true
}

func (s State) isOff() bool {
	return s.Volatile.RunningSlot == None
}

func (s *State) resetVolatile() {
	s.Volatile = Volatile{
		RunningSlot:    None,
		RunningVersion: Empty,
	}
}

type stateKey [8]uint8

func (s State) key() stateKey {
	return stateKey{
		uint8(s.Persistent.Image[A]),
		uint8(s.Persistent.Image[B]),
		uint8(s.Persistent.BootSlot),
		uint8(s.Persistent.Confirmed),
		uint8(s.Persistent.Fuse),
		uint8(s.Volatile.RunningSlot),
		uint8(s.Volatile.RunningVersion),
		uint8(MaxVersion),
	}
}

func (s State) String() string {
	return fmt.Sprintf(
		"P={A:%s B:%s boot:%s confirmed:%s fuse:%s} V={%s}",
		s.Persistent.Image[A],
		s.Persistent.Image[B],
		s.Persistent.BootSlot,
		s.Persistent.Confirmed,
		s.Persistent.Fuse,
		s.volatileString(),
	)
}

func (s State) volatileString() string {
	if s.isOff() {
		return "off"
	}
	return fmt.Sprintf("running=%s:%s", s.Volatile.RunningSlot, s.Volatile.RunningVersion)
}

func (slot Slot) String() string {
	switch slot {
	case A:
		return "A"
	case B:
		return "B"
	case None:
		return "none"
	default:
		return "invalid"
	}
}

func (version Version) String() string {
	switch version {
	case Empty:
		return "empty"
	default:
		return "v" + strconv.Itoa(int(version))
	}
}

func (kind Kind) String() string {
	switch kind {
	case Erase:
		return "erase"
	case Write:
		return "write"
	case SetBoot:
		return "set_boot"
	case Boot:
		return "boot"
	case Confirm:
		return "confirm"
	case FuseIncrement:
		return "fuse_increment"
	case PowerLoss:
		return "power_loss"
	default:
		return "invalid"
	}
}

func (event Event) String() string {
	switch event.Kind {
	case Write:
		return fmt.Sprintf("%s:%s:%s", event.Kind, event.Slot, event.Version)
	case Erase, SetBoot:
		return fmt.Sprintf("%s:%s", event.Kind, event.Slot)
	default:
		return event.Kind.String()
	}
}

func ParseEvent(text string) (Event, error) {
	parts := splitColon(text)
	if len(parts) < 1 || len(parts) > 3 {
		return Event{}, fmt.Errorf("invalid event %q", text)
	}

	kindToken := parts[0]
	slot := None
	if len(parts) > 1 {
		switch parts[1] {
		case "A":
			slot = A
		case "B":
			slot = B
		default:
			return Event{}, fmt.Errorf("invalid slot %q", parts[1])
		}
	}

	version := Empty
	if len(parts) > 2 {
		versionText := strings.TrimPrefix(parts[2], "v")
		parsed, err := strconv.Atoi(versionText)
		if err != nil || Version(parsed) < V1 || Version(parsed) > MaxVersion {
			return Event{}, fmt.Errorf("invalid version %q", parts[2])
		}
		version = Version(parsed)
	}

	switch kindToken {
	case "erase", "set_boot":
		if slot == None {
			return Event{}, fmt.Errorf("event %q requires a slot", text)
		}
		if len(parts) > 2 {
			return Event{}, fmt.Errorf("event %q takes no version", text)
		}
		if kindToken == "erase" {
			return Event{Kind: Erase, Slot: slot}, nil
		}
		return Event{Kind: SetBoot, Slot: slot}, nil

	case "write":
		if slot == None || version == Empty {
			return Event{}, fmt.Errorf("event %q requires a slot and version", text)
		}
		return Event{Kind: Write, Slot: slot, Version: version}, nil

	case "boot", "confirm", "fuse_increment", "power_loss":
		if len(parts) > 1 {
			return Event{}, fmt.Errorf("event %q takes no arguments", text)
		}
		return Event{Kind: kindByName(kindToken)}, nil

	default:
		return Event{}, fmt.Errorf("unknown event %q", kindToken)
	}
}

func kindByName(name string) Kind {
	switch name {
	case "boot":
		return Boot
	case "confirm":
		return Confirm
	case "fuse_increment":
		return FuseIncrement
	case "power_loss":
		return PowerLoss
	default:
		return Erase
	}
}

func splitColon(text string) []string {
	var parts []string
	start := 0
	for index, char := range text {
		if char == ':' {
			parts = append(parts, text[start:index])
			start = index + 1
		}
	}
	return append(parts, text[start:])
}
