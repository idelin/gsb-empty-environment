package netsec

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	mathrand "math/rand"
	"os"
	"path/filepath"
	"strings"
)

// CredentialEnvelope is the on-disk representation of an identity file. The
// envelope is self-describing so a file delivered late still carries the
// instance id, boot nonce and generation it was minted for.
type CredentialEnvelope struct {
	Kind       string `json:"kind"`
	InstanceID string `json:"instance_id"`
	Address    string `json:"address"`
	Nonce      string `json:"nonce"`
	Generation int64  `json:"generation"`
	Material   []byte `json:"material"`
}

func (e CredentialEnvelope) toCredential() (Credential, error) {
	cred := Credential{
		Kind:       CredentialKind(e.Kind),
		InstanceID: InstanceID(e.InstanceID),
		Address:    Address(e.Address),
		Nonce:      BootNonce(e.Nonce),
		Generation: Generation(e.Generation),
		Material:   append([]byte(nil), e.Material...),
	}
	if err := cred.validate(); err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// WriteCredentialDir writes each envelope to dir as an independently named
// identity file. Random suffixes mean directory ordering is unrelated to
// creation time, which models a network that may reorder deliveries.
func WriteCredentialDir(dir string, envelopes []CredentialEnvelope) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("bootstrap: create credential dir: %w", err)
	}

	paths := make([]string, 0, len(envelopes))
	for i, envelope := range envelopes {
		name := fmt.Sprintf("cred-%03d-%s.json", i, randomSuffix())
		path := filepath.Join(dir, name)

		payload, err := json.MarshalIndent(envelope, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("bootstrap: encode credential envelope: %w", err)
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			return nil, fmt.Errorf("bootstrap: write credential file: %w", err)
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func randomSuffix() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand failure is not recoverable for identity material.
		panic(fmt.Sprintf("bootstrap: cannot read random source: %v", err))
	}
	return hex.EncodeToString(raw[:])
}

// DeliveryOutcome is the result of feeding one file through the state machine.
type DeliveryOutcome struct {
	Path     string
	Decision Decision
	Err      error
}

// DeliverCredentialDir consumes every *.json identity file in dir in shuffled
// order, exercising the state machine through realistic out-of-order and
// replayed delivery. A file that cannot be parsed or validated is reported as
// DecisionRejected without ever touching the running environment.
func (s *Supervisor) DeliverCredentialDir(dir string) ([]DeliveryOutcome, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: read credential dir: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}

	// Shuffle so processing order cannot be confused with issuance order:
	// correctness must come from the binding fields, not arrival sequence.
	mathrand.Shuffle(len(files), func(i, j int) {
		files[i], files[j] = files[j], files[i]
	})

	outcomes := make([]DeliveryOutcome, 0, len(files))
	for _, path := range files {
		outcome := s.deliverFile(path)
		outcomes = append(outcomes, outcome)
	}
	return outcomes, nil
}

func (s *Supervisor) deliverFile(path string) DeliveryOutcome {
	payload, err := os.ReadFile(path)
	if err != nil {
		return DeliveryOutcome{Path: path, Decision: DecisionRejected, Err: err}
	}

	var envelope CredentialEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return DeliveryOutcome{
			Path:     path,
			Decision: DecisionRejected,
			Err:      fmt.Errorf("bootstrap: decode %s: %w", path, err),
		}
	}

	cred, err := envelope.toCredential()
	if err != nil {
		return DeliveryOutcome{Path: path, Decision: DecisionRejected, Err: err}
	}

	decision, err := s.HandleCredential(cred)
	return DeliveryOutcome{Path: path, Decision: decision, Err: err}
}
