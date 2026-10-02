package tracevault

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	defaultEpochSeconds = 3600
	defaultGridSize     = 0.01
	minKeyBytes         = 16
)

// Config holds the vault's key material and indexing parameters.
type Config struct {
	MasterKey    []byte  // derives pseudonyms and per-record encryption keys
	RegulatorKey []byte  // verifies judicial audit credentials
	Salt         []byte  // salts the spatial grid index
	EpochSeconds int64   // pseudonym rotation interval (default 3600)
	GridSize     float64 // grid cell side in degrees (default 0.01)
}

// Vault is the privacy-preserving spatio-temporal retrieval component.
type Vault struct {
	masterKey    []byte
	regulatorKey []byte
	salt         []byte
	epochSeconds int64
	gridSize     float64
	store        *Store
	audit        *AuditLog
}

func NewVault(cfg Config) (*Vault, error) {
	if len(cfg.MasterKey) < minKeyBytes {
		return nil, errors.New("tracevault: master key too short")
	}
	if len(cfg.RegulatorKey) < minKeyBytes {
		return nil, errors.New("tracevault: regulator key too short")
	}
	if len(cfg.Salt) < minKeyBytes {
		return nil, errors.New("tracevault: salt too short")
	}
	epochSeconds := cfg.EpochSeconds
	if epochSeconds <= 0 {
		epochSeconds = defaultEpochSeconds
	}
	gridSize := cfg.GridSize
	if gridSize <= 0 {
		gridSize = defaultGridSize
	}
	return &Vault{
		masterKey:    cfg.MasterKey,
		regulatorKey: cfg.RegulatorKey,
		salt:         cfg.Salt,
		epochSeconds: epochSeconds,
		gridSize:     gridSize,
		store:        NewStore(),
		audit:        NewAuditLog(),
	}, nil
}

// Point is a plaintext location sample submitted for storage.
type Point struct {
	EntityID  string
	Lat       float64
	Lon       float64
	Timestamp time.Time
}

// PlainPoint is a decrypted trajectory point returned by Reveal.
type PlainPoint struct {
	EntityID  string
	Lat       float64
	Lon       float64
	Timestamp time.Time
	Epoch     int64
}

type payload struct {
	EntityID  string    `json:"id"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
	Timestamp time.Time `json:"ts"`
}

// EpochOf maps a timestamp to its epoch bucket.
func (v *Vault) EpochOf(t time.Time) int64 {
	return t.Unix() / v.epochSeconds
}

// Ingest encrypts and indexes one location sample. The stored record carries
// only a rotating pseudonym, a salted cell hash and an AES-GCM ciphertext.
func (v *Vault) Ingest(p Point) error {
	if p.EntityID == "" {
		return errors.New("tracevault: empty entity id")
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return fmt.Errorf("tracevault: coordinate out of range: %f,%f", p.Lat, p.Lon)
	}
	epoch := v.EpochOf(p.Timestamp)
	pseudonym := Pseudonym(v.masterKey, p.EntityID, epoch)
	cellHash := CellHash(v.salt, CellID(p.Lat, p.Lon, v.gridSize))

	plaintext, err := json.Marshal(payload{
		EntityID:  p.EntityID,
		Lat:       p.Lat,
		Lon:       p.Lon,
		Timestamp: p.Timestamp.UTC(),
	})
	if err != nil {
		return err
	}
	recordKey := deriveKey(v.masterKey, "record", p.EntityID, fmtEpoch(epoch))
	ciphertext, err := encryptAESGCM(recordKey, plaintext, []byte(pseudonym))
	if err != nil {
		return err
	}
	v.store.Add(Record{
		Pseudonym:  pseudonym,
		Epoch:      epoch,
		CellHash:   cellHash,
		Ciphertext: ciphertext,
	})
	return nil
}

// Heatmap returns the anonymized aggregate heat distribution for a time
// window: salted cell hash -> sample count. This is the only output of
// day-to-day read-only queries.
func (v *Vault) Heatmap(from, to time.Time) map[string]int {
	return v.store.Heatmap(v.EpochOf(from), v.EpochOf(to))
}

// Reveal performs targeted deanonymization of a single entity under a valid
// judicial audit credential. Every attempt, successful or not, is recorded in
// the tamper-evident audit log.
func (v *Vault) Reveal(cred Credential, now time.Time) ([]PlainPoint, error) {
	if err := cred.Verify(v.regulatorKey, now); err != nil {
		v.audit.Append("REVEAL_DENIED", cred.Issuer, cred.EntityID, cred.FromEpoch, cred.ToEpoch, 0, cred.Nonce, now)
		return nil, err
	}
	var points []PlainPoint
	for epoch := cred.FromEpoch; epoch <= cred.ToEpoch; epoch++ {
		pseudonym := Pseudonym(v.masterKey, cred.EntityID, epoch)
		recordKey := deriveKey(v.masterKey, "record", cred.EntityID, fmtEpoch(epoch))
		for _, rec := range v.store.ByPseudonym(pseudonym, epoch, epoch) {
			plaintext, err := decryptAESGCM(recordKey, rec.Ciphertext, []byte(rec.Pseudonym))
			if err != nil {
				return nil, fmt.Errorf("tracevault: decrypt record at epoch %d: %w", epoch, err)
			}
			var pl payload
			if err := json.Unmarshal(plaintext, &pl); err != nil {
				return nil, err
			}
			points = append(points, PlainPoint{
				EntityID:  pl.EntityID,
				Lat:       pl.Lat,
				Lon:       pl.Lon,
				Timestamp: pl.Timestamp,
				Epoch:     epoch,
			})
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Timestamp.Before(points[j].Timestamp) })
	v.audit.Append("REVEAL", cred.Issuer, cred.EntityID, cred.FromEpoch, cred.ToEpoch, len(points), cred.Nonce, now)
	return points, nil
}

// AuditLog exposes the vault's append-only audit trail.
func (v *Vault) AuditLog() *AuditLog { return v.audit }

// StoreLen reports how many encrypted records are held.
func (v *Vault) StoreLen() int { return v.store.Len() }
