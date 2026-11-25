package pinpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// digestString renders the policy body deterministically. Go's encoding/json
// sorts map keys, and all fields are fixed-order scalars or simple maps, so
// the digest is stable across processes and releases.
func canonicalPolicyBody(p Policy) string {
	roles := make([]string, 0, len(p.Thresholds))
	thresholds := make(map[string]int, len(p.Thresholds))
	for role, threshold := range p.Thresholds {
		roles = append(roles, string(role))
		thresholds[string(role)] = threshold
	}
	sort.Strings(roles)

	out := "policy{"
	out += "seq=" + strconv.FormatInt(int64(p.Sequence), 10)
	out += ";host=" + string(p.Host)
	out += ";from=" + strconv.FormatInt(int64(p.ValidFrom), 10)
	out += ";until=" + strconv.FormatInt(int64(p.ValidUntil), 10)
	out += ";primary=" + string(p.Primary)
	out += ";backup=" + string(p.Backup)
	out += ";root=" + string(p.RootKey)
	out += ";prev=" + p.PrevDigest
	out += ";thresholds={"
	for index, role := range roles {
		if index > 0 {
			out += ","
		}
		out += role + "=" + strconv.Itoa(thresholds[role])
	}
	out += "}}"
	return out
}

// Digest returns the canonical hex SHA-256 digest of a policy body. Handover
// authorizations and predecessor links reference this string.
func Digest(p Policy) string {
	sum := sha256.Sum256([]byte(canonicalPolicyBody(p)))
	return hex.EncodeToString(sum[:])
}
