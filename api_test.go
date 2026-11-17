package netsec_test

import (
	"encoding/binary"
	"testing"

	netsec "example.org/netsec/q0332"
)

type onePageStore struct {
	data []byte
}

func (s onePageStore) PageSize() uint64 { return 64 }

func (s onePageStore) ReadPage(id netsec.PageID) ([]byte, error) {
	if id != 0 {
		return nil, nil
	}
	return s.data, nil
}

func TestRootPackageAPI(t *testing.T) {
	page := make([]byte, 64)
	binary.LittleEndian.PutUint64(page, uint64(1)|(uint64(0x40)<<8))

	auth, err := netsec.NewRegionSet(netsec.Region{
		Start: 0x40,
		Size:  8,
		Allow: netsec.AccessWrite,
	})
	if err != nil {
		t.Fatal(err)
	}
	format := &netsec.Format{
		EntrySize: 16,
		Opcode:    netsec.OpcodeField{BitOffset: 0, BitWidth: 8},
		Patterns: []netsec.Pattern{{
			Name:  "write",
			Value: 1,
			Fields: []netsec.AddressField{{
				Name:       "dst",
				BitOffset:  8,
				BitWidth:   56,
				Kind:       netsec.FieldWrite,
				AccessSize: 8,
			}},
		}},
	}
	plan, err := netsec.Validate(onePageStore{data: page}, auth, netsec.Request{
		Address: 0,
		Length:  16,
		Format:  format,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Writes(); len(got) != 1 || got[0].Address != 0x40 {
		t.Fatalf("unexpected writes: %+v", got)
	}
}
