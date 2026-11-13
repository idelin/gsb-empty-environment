package netsec

import (
	"fmt"
	"strings"
)

// Authority is the minimal local-authoritative data source used by the
// admission handler. A lookup's boolean result reports whether the question
// belongs to the authority at all.
type Authority interface {
	Lookup(question Question) (Resolution, bool)
}

// Zone is an immutable local authoritative DNS zone keyed by canonical
// lower-case names.
type Zone struct {
	origin   string
	records  []Record
	byName   map[string][]Record
	existing map[string]struct{}
	soa      *Record
}

// NewZone validates and builds an authoritative zone. Records outside origin
// are rejected so a misconfiguration cannot accidentally expose resolver
// cache data as authoritative data.
func NewZone(origin string, records ...Record) (*Zone, error) {
	canonicalOrigin, err := canonicalName(origin)
	if err != nil {
		return nil, fmt.Errorf("netsec: invalid zone origin %q: %w", origin, err)
	}
	zone := &Zone{
		origin:   canonicalOrigin,
		records:  make([]Record, 0, len(records)),
		byName:   make(map[string][]Record),
		existing: make(map[string]struct{}),
	}
	for _, record := range records {
		owner, err := canonicalName(record.Name)
		if err != nil {
			return nil, fmt.Errorf("netsec: invalid record owner %q: %w", record.Name, err)
		}
		if !zone.contains(owner) {
			return nil, fmt.Errorf("netsec: record %q is outside zone %q", owner, canonicalOrigin)
		}
		if record.Class == 0 {
			record.Class = ClassINET
		}
		record.Name = owner
		record.Data = append([]byte(nil), record.Data...)
		if err := validateZoneRecord(record); err != nil {
			return nil, err
		}
		if record.Type == TypeSOA && owner == canonicalOrigin && zone.soa == nil {
			copied := record
			zone.soa = &copied
		}
		zone.records = append(zone.records, record)
		zone.byName[owner] = append(zone.byName[owner], record)
		zone.markExisting(owner)
	}
	return zone, nil
}

// Origin returns the canonical zone origin.
func (z *Zone) Origin() string {
	if z == nil {
		return ""
	}
	return z.origin
}

// Records returns a copy of the zone's records.
func (z *Zone) Records() []Record {
	if z == nil {
		return nil
	}
	return cloneRecords(z.records)
}

// Contains reports whether name belongs to this zone.
func (z *Zone) Contains(name string) bool {
	if z == nil {
		return false
	}
	canonical, err := canonicalName(name)
	if err != nil {
		return false
	}
	return z.contains(canonical)
}

func (z *Zone) contains(name string) bool {
	if z.origin == "." {
		return true
	}
	return name == z.origin || strings.HasSuffix(name, "."+z.origin)
}

func (z *Zone) markExisting(name string) {
	for {
		z.markName(name)
		if name == z.origin || name == "." {
			return
		}
		index := strings.IndexByte(name, '.')
		if index < 0 || index+1 >= len(name) {
			return
		}
		name = name[index+1:]
	}
}

func (z *Zone) markName(name string) {
	z.existing[name] = struct{}{}
}

// Lookup answers from local authoritative data only. It never invokes a
// cache or recursive upstream.
func (z *Zone) Lookup(question Question) (Resolution, bool) {
	if z == nil {
		return Resolution{}, false
	}
	name, err := canonicalName(question.Name)
	if err != nil || !z.contains(name) {
		return Resolution{}, false
	}
	class := question.Class
	if class == 0 {
		class = ClassINET
	}
	if class != ClassINET {
		return Resolution{Authoritative: true, RCode: RCodeNotImp}, true
	}

	if question.Type == TypeANY {
		if records := z.byName[name]; len(records) > 0 {
			return Resolution{
				Authoritative: true,
				Answer:        cloneRecords(records),
			}, true
		}
		return z.negative(name), true
	}

	if records := z.typeRecords(name, question.Type); len(records) > 0 {
		return Resolution{Authoritative: true, Answer: cloneRecords(records)}, true
	}

	if question.Type != TypeCNAME {
		if chain := z.cnameChain(name, question.Type); len(chain) > 0 {
			return Resolution{Authoritative: true, Answer: chain}, true
		}
	}
	return z.negative(name), true
}

func (z *Zone) typeRecords(name string, recordType uint16) []Record {
	var out []Record
	for _, record := range z.byName[name] {
		if record.Type == recordType {
			out = append(out, record)
		}
	}
	return out
}

func (z *Zone) cnameChain(name string, recordType uint16) []Record {
	current := name
	seen := map[string]struct{}{current: {}}
	var answer []Record
	for depth := 0; depth < 16; depth++ {
		cnames := z.typeRecords(current, TypeCNAME)
		if len(cnames) == 0 {
			if records := z.typeRecords(current, recordType); len(records) > 0 {
				if current == name {
					return nil
				}
				answer = append(answer, records...)
			}
			return answer
		}
		answer = append(answer, cnames[0])
		target, err := decodeRDataName(cnames[0].Data)
		if err != nil {
			return answer
		}
		target, err = canonicalName(target)
		if err != nil {
			return answer
		}
		if _, duplicate := seen[target]; duplicate {
			return answer
		}
		seen[target] = struct{}{}
		current = target
		if !z.contains(current) {
			return answer
		}
		if records := z.typeRecords(current, recordType); len(records) > 0 {
			answer = append(answer, records...)
			return answer
		}
	}
	return answer
}

func (z *Zone) negative(name string) Resolution {
	response := Resolution{Authoritative: true, RCode: RCodeNXDomain}
	if _, exists := z.existing[name]; exists {
		response.RCode = RCodeNoError
	}
	if z.soa != nil {
		response.Authority = cloneRecords([]Record{*z.soa})
	}
	return response
}

func validateZoneRecord(record Record) error {
	if record.Type == 0 || record.Type == TypeOPT {
		return fmt.Errorf("netsec: record %q has unsupported type %d", record.Name, record.Type)
	}
	switch record.Type {
	case TypeA:
		if len(record.Data) != 4 {
			return fmt.Errorf("netsec: A record %q has invalid RDATA length", record.Name)
		}
	case TypeAAAA:
		if len(record.Data) != 16 {
			return fmt.Errorf("netsec: AAAA record %q has invalid RDATA length", record.Name)
		}
	case TypeCNAME, TypeNS, TypePTR:
		if _, err := decodeRDataName(record.Data); err != nil {
			return fmt.Errorf("netsec: record %q has invalid domain-name RDATA: %w", record.Name, err)
		}
	}
	return nil
}
