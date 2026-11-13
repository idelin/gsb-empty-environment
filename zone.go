package netsec

// Zone is the local zone content visible to the transfer admission layer.
// Records contains exactly one SOA record at the zone apex plus the zone's
// other records.
type Zone struct {
	Name    string
	Records []Record
}

// SOA returns the zone's SOA record. It returns false when the zone has no
// SOA or when more than one SOA is present.
func (z Zone) SOA() (Record, bool) {
	var found Record
	count := 0
	for _, record := range z.Records {
		if record.IsSOA() {
			found = record
			count++
		}
	}
	if count != 1 {
		return Record{}, false
	}
	return found, true
}

// Serial returns the zone's current SOA serial.
func (z Zone) Serial() (uint32, bool) {
	soa, ok := z.SOA()
	if !ok {
		return 0, false
	}
	return soa.SOA.Serial, true
}

// Clone returns a deep copy of the zone.
func (z Zone) Clone() Zone {
	return Zone{
		Name:    z.Name,
		Records: cloneRecords(z.Records),
	}
}
