package netsec

import (
	"fmt"
	"regexp"
	"strings"
)

// sha1Commit matches a full, non-abbreviated Git object name: exactly 40
// lowercase or uppercase hexadecimal characters.
var sha1Commit = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// validateSource enforces the source policy:
//
//   - :http (or :https archive) sources are rejected because they download an
//     unversioned archive that cannot be tied to an immutable commit.
//   - :git sources must pin a full 40-character SHA-1 via :commit; a tag,
//     branch or moving ref is not accepted.
//   - any other source type is rejected.
func validateSource(source map[string]any) error {
	if _, ok := source["http"]; ok {
		return fmt.Errorf("%w: %v", ErrHTTPSource, source["http"])
	}

	gitURL, ok := source["git"]
	if !ok {
		return fmt.Errorf("%w: source is neither http nor git", ErrUnsupportedSource)
	}

	url, _ := gitURL.(string)
	if url == "" {
		return fmt.Errorf("%w: git url is empty", ErrUnsupportedSource)
	}
	if strings.HasPrefix(strings.ToLower(url), "http://") {
		return fmt.Errorf("%w: git url must not use plain http: %s", ErrUnsupportedSource, url)
	}

	commit, ok := source["commit"].(string)
	if !ok || !sha1Commit.MatchString(commit) {
		return fmt.Errorf("%w: %s", ErrGitCommitNotPinned, url)
	}
	return nil
}
