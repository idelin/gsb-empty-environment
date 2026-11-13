// Package netsec verifies Git notes branches that carry release-gate
// approvals.
//
// Git stores a note for target commit X at a hexadecimal fan-out path such as
// "ab/cdef...". The same target can be spelled with different fan-out cuts
// ("ab/cdef..." vs "abc/def..."), and notes commits are not necessarily
// signed. A release gate therefore cannot trust a note blob merely because the
// current tree contains it: it must prove that the exact current blob content
// was first introduced at that path by a commit signed by an authorized
// signer, and that no conflicting path spelling exists for the target.
package netsec

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// approvalPrefix is the mandatory first-line trailer of an approval blob.
const approvalPrefix = "Approved-by: "

// emailPattern accepts a plain "local@domain.tld" address. The whole remainder
// of the first line must be the email; the "Name <email>" trailer form is not
// accepted.
var emailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

// Commit describes one commit on the notes branch.
//
// Commits must be supplied in chronological order, oldest first. Paths maps
// every notes-relative path in the commit's tree to the blob ID stored there.
type Commit struct {
	ID       string
	Signer   string
	Verified bool
	Paths    map[string]string
}

// Input is the material needed to validate a notes branch.
type Input struct {
	// Commits is the full notes branch commit chain, oldest first.
	Commits []Commit

	// Blobs maps blob ID to the raw blob content referenced by any tree.
	Blobs map[string][]byte

	// Authorized holds the signer identities (emails) the caller trusts to
	// create approvals. A signer must both be present here and have a verified
	// signature on the introducing commit.
	Authorized map[string]bool
}

// ViolationCode classifies a validation failure.
type ViolationCode string

const (
	// ViolationInvalidPath means a tip-tree path does not normalize to a
	// 40-character lowercase hexadecimal target commit ID.
	ViolationInvalidPath ViolationCode = "invalid_path"

	// ViolationPathConflict means two distinct tip-tree paths normalize to the
	// same target commit ID; every note for that target is discarded.
	ViolationPathConflict ViolationCode = "path_conflict"

	// ViolationBlobMissing means the tip tree references a blob whose content
	// was not supplied.
	ViolationBlobMissing ViolationCode = "blob_missing"

	// ViolationUntrustedHistory means the current blob content first appeared
	// at the path in a commit that was unsigned, failed verification, or was
	// signed by a signer outside the authorized set.
	ViolationUntrustedHistory ViolationCode = "untrusted_introduction"

	// ViolationBadApprovalLine means a trusted blob does not start with a
	// well-formed "Approved-by: email" line.
	ViolationBadApprovalLine ViolationCode = "bad_approval_line"
)

// Violation records one rejected note or path.
type Violation struct {
	Code   ViolationCode
	Target string
	Path   string
	Detail string
}

// Approval is a note that passed every check.
type Approval struct {
	Target       string
	Path         string
	BlobID       string
	ApprovedBy   string
	IntroducedBy string
}

// Report is the validation result.
type Report struct {
	Approvals  map[string]Approval
	Violations []Violation
}

// BlobID returns the Git object ID (SHA-1) of a blob with the given content.
func BlobID(content []byte) string {
	hasher := sha1.New()
	fmt.Fprintf(hasher, "blob %d\x00", len(content))
	hasher.Write(content)
	return hex.EncodeToString(hasher.Sum(nil))
}

// Validate inspects the supplied notes branch and returns every approval that
// can be trusted alongside all violations found.
//
// An error is returned only when the input itself is inconsistent (an empty
// commit chain or a blob whose ID does not hash its content). Rejected notes
// are reported as violations, not errors.
func Validate(in Input) (*Report, error) {
	if len(in.Commits) == 0 {
		return nil, fmt.Errorf("netsec: notes branch has no commits")
	}

	for id, content := range in.Blobs {
		if len(id) != 40 || !isLowerHex(id) {
			return nil, fmt.Errorf("netsec: malformed blob ID %q", id)
		}
		if BlobID(content) != id {
			return nil, fmt.Errorf("netsec: blob %q content does not match its object ID", id)
		}
	}

	report := &Report{Approvals: make(map[string]Approval)}
	tip := in.Commits[len(in.Commits)-1]

	type entry struct {
		path   string
		target string
		blobID string
	}

	paths := make([]string, 0, len(tip.Paths))
	for path := range tip.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	grouped := make(map[string][]entry)
	for _, path := range paths {
		target := strings.ReplaceAll(path, "/", "")
		if len(target) != 40 || !isLowerHex(target) {
			report.Violations = append(report.Violations, Violation{
				Code:   ViolationInvalidPath,
				Path:   path,
				Detail: "path without '/' separators must be 40 lowercase hexadecimal characters",
			})
			continue
		}
		grouped[target] = append(grouped[target], entry{
			path:   path,
			target: target,
			blobID: tip.Paths[path],
		})
	}

	targets := make([]string, 0, len(grouped))
	for target := range grouped {
		targets = append(targets, target)
	}
	sort.Strings(targets)

	for _, target := range targets {
		entries := grouped[target]
		if len(entries) > 1 {
			spellings := make([]string, 0, len(entries))
			for _, item := range entries {
				spellings = append(spellings, item.path)
			}
			report.Violations = append(report.Violations, Violation{
				Code:   ViolationPathConflict,
				Target: target,
				Detail: "conflicting path spellings: " + strings.Join(spellings, ", "),
			})
			continue
		}

		item := entries[0]
		content, ok := in.Blobs[item.blobID]
		if !ok {
			report.Violations = append(report.Violations, Violation{
				Code:   ViolationBlobMissing,
				Target: target,
				Path:   item.path,
				Detail: "blob " + item.blobID + " content was not provided",
			})
			continue
		}

		introducer := firstIntroducingCommit(in.Commits, item.path, item.blobID)
		if introducer == nil || !introducer.Verified || !in.Authorized[introducer.Signer] {
			detail := "current blob content was not introduced by a verified authorized signer"
			if introducer != nil {
				detail = fmt.Sprintf("introduced by commit %s (signer %q, verified %t)",
					introducer.ID, introducer.Signer, introducer.Verified)
			}
			report.Violations = append(report.Violations, Violation{
				Code:   ViolationUntrustedHistory,
				Target: target,
				Path:   item.path,
				Detail: detail,
			})
			continue
		}

		approvedBy, ok := parseApprovalLine(content)
		if !ok {
			report.Violations = append(report.Violations, Violation{
				Code:   ViolationBadApprovalLine,
				Target: target,
				Path:   item.path,
				Detail: "first line must be 'Approved-by: email'",
			})
			continue
		}

		report.Approvals[target] = Approval{
			Target:       target,
			Path:         item.path,
			BlobID:       item.blobID,
			ApprovedBy:   approvedBy,
			IntroducedBy: introducer.ID,
		}
	}

	sort.SliceStable(report.Violations, func(i, j int) bool {
		if report.Violations[i].Code != report.Violations[j].Code {
			return report.Violations[i].Code < report.Violations[j].Code
		}
		if report.Violations[i].Target != report.Violations[j].Target {
			return report.Violations[i].Target < report.Violations[j].Target
		}
		return report.Violations[i].Path < report.Violations[j].Path
	})

	return report, nil
}

// firstIntroducingCommit returns the oldest commit whose tree stores blobID at
// path. Earlier commits carrying different content at the same path do not
// qualify: only the first appearance of the current blob content matters.
func firstIntroducingCommit(commits []Commit, path, blobID string) *Commit {
	for index := range commits {
		if commits[index].Paths[path] == blobID {
			return &commits[index]
		}
	}
	return nil
}

// parseApprovalLine extracts the email from a blob whose first line has the
// form "Approved-by: email".
func parseApprovalLine(content []byte) (string, bool) {
	firstLine, _, _ := strings.Cut(string(content), "\n")
	firstLine = strings.TrimSuffix(firstLine, "\r")
	if !strings.HasPrefix(firstLine, approvalPrefix) {
		return "", false
	}
	email := strings.TrimSpace(firstLine[len(approvalPrefix):])
	if !emailPattern.MatchString(email) {
		return "", false
	}
	return email, true
}

// isLowerHex reports whether s contains only lowercase hexadecimal characters.
func isLowerHex(s string) bool {
	for index := 0; index < len(s); index++ {
		switch c := s[index]; {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
