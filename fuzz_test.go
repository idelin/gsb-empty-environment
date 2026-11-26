package safeunpack

import "testing"

func FuzzValidateArchiveDoesNotPanic(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("PK\x03\x04"))
	f.Add([]byte("PK\x05\x06\x00\x00\x00\x00"))
	f.Add([]byte("\x1f\x8b\x08\x00"))
	f.Add(make([]byte, tarBlockSize*2))
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = ValidateArchiveBytes(
			data,
			WithMaxArchiveBytes(1<<20),
			WithMaxFileBytes(1<<20),
			WithMaxTotalBytes(2<<20),
			WithMaxEntries(1024),
		)
	})
}
