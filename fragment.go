package netsec

import (
	"bytes"
	"time"
)

// Fragment is one piece of a multi-fragment application request.  Every
// fragment in an assembly must repeat the same identity header; fragments are
// accepted strictly in index order and the request is frozen only after the
// final fragment has arrived.
type Fragment struct {
	MasterAddress        uint16
	OutstationAddress    uint16
	ConnectionGeneration uint64
	ApplicationSequence  uint32
	FunctionCode         uint8
	ObjectGroup          uint8
	ObjectVariation      uint8
	ObjectIndex          uint32
	FragmentIndex        uint32
	Final                bool
	Data                 []byte
}

func (f Fragment) header() fragmentHeader {
	return fragmentHeader{
		connection: ConnectionID{
			MasterAddress:        f.MasterAddress,
			OutstationAddress:    f.OutstationAddress,
			ConnectionGeneration: f.ConnectionGeneration,
		},
		applicationSequence: f.ApplicationSequence,
		functionCode:        f.FunctionCode,
		objectGroup:         f.ObjectGroup,
		objectVariation:     f.ObjectVariation,
		objectIndex:         f.ObjectIndex,
	}
}

func (f Fragment) assemblyKey() fragmentKey {
	return fragmentKey{
		connection:          f.header().connection,
		applicationSequence: f.ApplicationSequence,
	}
}

func (f Fragment) identity(value []byte) RequestIdentity {
	return RequestIdentity{
		MasterAddress:        f.MasterAddress,
		OutstationAddress:    f.OutstationAddress,
		ConnectionGeneration: f.ConnectionGeneration,
		ApplicationSequence:  f.ApplicationSequence,
		FunctionCode:         f.FunctionCode,
		ObjectGroup:          f.ObjectGroup,
		ObjectVariation:      f.ObjectVariation,
		ObjectIndex:          f.ObjectIndex,
		OperationValue:       value,
	}
}

type fragmentHeader struct {
	connection          ConnectionID
	applicationSequence uint32
	functionCode        uint8
	objectGroup         uint8
	objectVariation     uint8
	objectIndex         uint32
}

type fragmentKey struct {
	connection          ConnectionID
	applicationSequence uint32
}

type assembly struct {
	header    fragmentHeader
	nextIndex uint32
	buffer    bytes.Buffer
	deadline  time.Time
}
