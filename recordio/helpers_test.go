package recordio

import "encoding/binary"

func binaryPutU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func binaryPutU16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }
