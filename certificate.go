package delegation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

type PathState struct {
	ID         int   `json:"id"`
	RuleState  []int `json:"rule_state"`
	Transition []int `json:"transition"`
}

type StateOutput struct {
	PathStateID      int      `json:"path_state_id"`
	TagSet           []string `json:"tag_set"`
	TimeSegmentIndex int      `json:"time_segment_index"`
	LocalEffect      Effect   `json:"local_effect"`
	LastRule         *RuleRef `json:"last_rule,omitempty"`
	Decision         Effect   `json:"decision"`
	Achieved         int      `json:"achieved"`
	AllowingParents  []string `json:"allowing_parents,omitempty"`
}

type DelegationCertificate struct {
	Hash                 string        `json:"hash,omitempty"`
	DelegationID         string        `json:"delegation_id"`
	Tenant               string        `json:"tenant"`
	Action               string        `json:"action"`
	QuorumK              int           `json:"quorum_k"`
	ParentCertificateIDs []string      `json:"parent_certificate_ids,omitempty"`
	PathAlphabet         []string      `json:"path_alphabet"`
	TimeSegments         []TimeSegment `json:"time_segments"`
	PathStates           []PathState   `json:"path_states"`
	Outputs              []StateOutput `json:"outputs"`
}

type CertificateBundle struct {
	RootID       string                            `json:"root_id"`
	Hash         string                            `json:"hash,omitempty"`
	Certificates map[string]*DelegationCertificate `json:"certificates"`
}

func (bundle *CertificateBundle) computeHash() (string, error) {
	copy := *bundle
	copy.Hash = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (bundle *CertificateBundle) SetHash() error {
	hash, err := bundle.computeHash()
	if err != nil {
		return err
	}
	bundle.Hash = hash
	return nil
}

func (bundle *CertificateBundle) VerifyHash() (bool, error) {
	hash, err := bundle.computeHash()
	if err != nil {
		return false, err
	}
	return hash == bundle.Hash, nil
}

func (cert *DelegationCertificate) computeHash() (string, error) {
	copy := *cert
	copy.Hash = ""
	encoded, err := json.Marshal(copy)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (cert *DelegationCertificate) setHash() error {
	hash, err := cert.computeHash()
	if err != nil {
		return err
	}
	cert.Hash = hash
	return nil
}

func (cert *DelegationCertificate) VerifyHash() (bool, error) {
	hash, err := cert.computeHash()
	if err != nil {
		return false, err
	}
	return hash == cert.Hash, nil
}
