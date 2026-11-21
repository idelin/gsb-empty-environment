package vex

import (
	"encoding/json"
	"fmt"
	"sort"
)

type evidenceFingerprint struct {
	Artifact      string            `json:"artifact,omitempty"`
	Vulnerability string            `json:"vulnerability,omitempty"`
	Marked        []ObjectID        `json:"marked,omitempty"`
	Allowed       []string          `json:"allowed,omitempty"`
	Candidates    []CandidateConfig `json:"candidates,omitempty"`
	Manifests     []LinkManifest    `json:"manifests,omitempty"`
	Summaries     []Object          `json:"summaries,omitempty"`
	Require       bool              `json:"requireSignatures"`
	MaxStates     int               `json:"maxBindingStates"`
}

// SigningPayload returns the canonical bytes covered by a symbol summary
// signature. Signature fields are excluded.
func (o Object) SigningPayload() ([]byte, error) {
	return json.Marshal(objectPayload(o))
}

// SigningPayload returns the canonical bytes covered by a link-manifest
// signature. Signature fields are excluded.
func (m LinkManifest) SigningPayload() ([]byte, error) {
	return json.Marshal(manifestPayload(m))
}

// SigningPayload returns the canonical bytes covered by a candidate
// configuration signature. Embedded manifests and signature fields are
// excluded; manifests are signed separately.
func (c CandidateConfig) SigningPayload() ([]byte, error) {
	return json.Marshal(candidatePayload(c))
}

// PayloadDigest returns the sha256 digest used by Signature.PayloadDigest.
func PayloadDigest(payload []byte) string {
	return sha256Hex(payload)
}

func evidenceDigest(a *analyzer) (string, error) {
	fp := evidenceFingerprint{
		Artifact:      a.ev.Artifact,
		Vulnerability: a.ev.Vulnerability,
		Marked:        append([]ObjectID(nil), a.marked...),
		Allowed:       append([]string(nil), a.allowed...),
		Require:       a.require,
		MaxStates:     a.maxStates,
	}
	for _, candidate := range a.candidateList {
		copy := candidate
		copy.Signature = signatureFingerprint(candidate.SignatureOr(candidate.Signed))
		if copy.Manifest != nil {
			manifest := *copy.Manifest
			manifest.Signature = signatureFingerprint(manifest.Signature)
			copy.Manifest = &manifest
		}
		fp.Candidates = append(fp.Candidates, copy)
	}
	fp.Manifests = append(fp.Manifests, a.manifests...)
	for i := range fp.Manifests {
		fp.Manifests[i].Signature = signatureFingerprint(fp.Manifests[i].Signature)
	}
	sort.Slice(fp.Manifests, func(i, j int) bool {
		return firstNonEmpty(fp.Manifests[i].Config, fp.Manifests[i].ID) <
			firstNonEmpty(fp.Manifests[j].Config, fp.Manifests[j].ID)
	})
	for _, obj := range a.summaries {
		copy := obj
		copy.Signature = signatureFingerprint(obj.Signature)
		fp.Summaries = append(fp.Summaries, copy)
	}
	sort.SliceStable(fp.Summaries, func(i, j int) bool {
		return idKey(fp.Summaries[i].ID) < idKey(fp.Summaries[j].ID)
	})
	return hashJSON(fp)
}

func signatureFingerprint(value any) any {
	switch sig := value.(type) {
	case nil:
		return nil
	case Signature:
		return sig
	case *Signature:
		if sig == nil {
			return nil
		}
		return *sig
	case string:
		return map[string]string{"value": sig}
	case []byte:
		return map[string]string{"value": string(sig)}
	case bool:
		return map[string]bool{"signed": sig}
	case json.RawMessage:
		return string(sig)
	case map[string]any:
		data, err := json.Marshal(sig)
		if err == nil {
			return data
		}
	}
	data, err := json.Marshal(value)
	if err == nil {
		return data
	}
	return fmt.Sprintf("%T", value)
}
