package apkverify

import "testing"

func FuzzParse(f *testing.F) {
	f.Add([]byte("dex\n035\x00"))
	f.Add([]byte{0x7f, 'E', 'L', 'F'})
	f.Add(buildAPK(f, validEntries(), []testScheme{validV2Scheme()}, ""))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = Parse(data)
	})
}
