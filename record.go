package testsel

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"strings"
)

// ListTests returns the names of all Test* functions in the package at dir,
// using "go test -list".
func ListTests(dir string) ([]string, error) {
	cmd := exec.Command("go", "test", "-list", ".*")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("testsel: go test -list: %v", err)
	}
	var tests []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Test") {
			tests = append(tests, line)
		}
	}
	return tests, nil
}

// RecordCoverage runs each Test* function in the package at dir in
// isolation and maps its coverage profile to functions via idx. The result
// can be stored directly in Snapshot.Coverage. pkgPath prefixes the test
// IDs so they are unique across packages.
//
// This is a convenience helper for small to medium suites; running one
// "go test" invocation per test is inherently O(tests x build).
func RecordCoverage(dir string, pkgPath string, idx *FuncIndex) (map[TestID][]FuncID, error) {
	tests, err := ListTests(dir)
	if err != nil {
		return nil, err
	}
	coverage := map[TestID][]FuncID{}
	for _, test := range tests {
		profile, err := runWithProfile(dir, test)
		if err != nil {
			return nil, err
		}
		blocks, err := ParseCoverProfile(profile)
		os.Remove(profile)
		if err != nil {
			return nil, err
		}
		covered := idx.MapBlocks(blocks)
		list := make([]FuncID, 0, len(covered))
		for id := range covered {
			list = append(list, id)
		}
		coverage[TestID(pkgPath+"."+test)] = sortedFuncs(list)
	}
	return coverage, nil
}

func runWithProfile(dir, test string) (string, error) {
	tmp, err := ioutil.TempFile("", "testsel-cover-*.out")
	if err != nil {
		return "", err
	}
	tmp.Close()
	cmd := exec.Command("go", "test", "-run", "^"+test+"$", "-count=1", "-coverprofile", tmp.Name())
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("testsel: go test -run %s: %v: %s", test, err, stderr.String())
	}
	return tmp.Name(), nil
}
