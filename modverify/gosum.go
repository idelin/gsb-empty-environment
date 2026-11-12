package modverify

import (
	"os"
	"strings"
)

// Reference holds the h1 hashes recorded for one module version by a single
// trust source (a local go.sum file or the verification database).
type Reference struct {
	ZipHash string // h1 hash of the module zip
	ModHash string // h1 hash of go.mod
}

// ParseGoSum extracts the recorded hashes for module@version from a go.sum
// file. It returns (nil, nil) when the file is absent or contains no entry
// for this module version, meaning the source is unavailable.
func ParseGoSum(goSumPath, module, version string) (*Reference, error) {
	data, err := os.ReadFile(goSumPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ref := &Reference{}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		if fields[0] != module {
			continue
		}
		switch fields[1] {
		case version:
			ref.ZipHash = fields[2]
			found = true
		case version + "/go.mod":
			ref.ModHash = fields[2]
			found = true
		}
	}
	if !found {
		return nil, nil
	}
	return ref, nil
}
