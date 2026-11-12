package relocation

import (
	"fmt"
	"strings"
)

// segmentBackslash is reported when a groupId segment contains a backslash.
// Backslashes are rejected before any filesystem path is built so that an
// attacker cannot smuggle Windows-style separators into a repository path.
const segmentBackslash = `\`

// CheckGroupID validates a Maven groupId before it is converted into a
// repository path. It rejects:
//   - empty groupIds and empty (leading, trailing, or doubled dot) segments,
//   - the ".." segment, which would escape the repository root,
//   - any segment containing a backslash.

func CheckGroupID(groupID string) error {
	if groupID == "" {
		return fmt.Errorf("relocation: empty groupId")
	}
	for _, segment := range strings.Split(groupID, ".") {
		if segment == "" {
			return fmt.Errorf("relocation: empty segment in groupId %q", groupID)
		}
		if segment == ".." {
			return fmt.Errorf("relocation: %q segment is not allowed in groupId %q", "..", groupID)
		}
		if strings.Contains(segment, segmentBackslash) {
			return fmt.Errorf("relocation: backslash is not allowed in groupId %q", groupID)
		}
	}
	return nil
}

// GroupPath converts a validated Maven groupId into the relative repository
// path (dots replaced by path separators). The groupId is validated first;
// callers must not build paths from a rejected groupId.
func GroupPath(groupID string) (string, error) {
	if err := CheckGroupID(groupID); err != nil {
		return "", err
	}
	return strings.ReplaceAll(groupID, ".", "/"), nil
}
