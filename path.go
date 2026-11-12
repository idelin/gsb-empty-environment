package netsec

import (
	"crypto/md5"
	"encoding/hex"
	"path"
	"path/filepath"
	"strings"
)

// SpecPath returns the repository-relative mirror path of a podspec using the
// CocoaPods CDN sharding scheme: the first three hex characters of the MD5
// digest of the pod name become three nested directories, for example
// Specs/d/a/2/Alamofire/5.6.4/Alamofire.podspec.json.
func SpecPath(name, version string) string {
	digest := md5.Sum([]byte(name))
	hexDigest := hex.EncodeToString(digest[:])
	return path.Join(
		"Specs",
		hexDigest[0:1],
		hexDigest[1:2],
		hexDigest[2:3],
		name,
		version,
		name+".podspec.json",
	)
}

// cleanRelPath normalizes a repo-relative path for comparison against
// SpecPath output.
func cleanRelPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	return path.Clean(p)
}

// safePathPart rejects names or versions that would let a podspec escape the
// sharded directory layout.
func safePathPart(part string) bool {
	if part == "" || part == "." || part == ".." {
		return false
	}
	if strings.ContainsAny(part, `/\`) {
		return false
	}
	return true
}
