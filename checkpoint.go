package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type checkpointBody struct {
	Version   int    `json:"version"`
	Seq       uint64 `json:"seq"`
	Head      Digest `json:"head"`
	CreatedAt string `json:"createdAt"`
}

type checkpointLine struct {
	checkpointBody
	MAC Digest `json:"hmac"`
}

// Checkpoint is a host-authenticated assertion that every transaction at or
// before Seq has already been completed or rolled back.  Recovery validates
// the suffix after this point and refuses to trust records that no longer
// connect to it.
type Checkpoint struct {
	Version   int
	Seq       uint64
	Head      Digest
	CreatedAt time.Time
	MAC       Digest
}

func checkpointMAC(key []byte, body checkpointBody) Digest {
	canonical, _ := json.Marshal(body)
	m := hmac.New(sha256.New, key)
	_, _ = m.Write(canonical)
	var d Digest
	copy(d[:], m.Sum(nil))
	return d
}

func appendCheckpoint(path string, key []byte, cp Checkpoint) (Checkpoint, error) {
	body := checkpointBody{
		Version:   1,
		Seq:       cp.Seq,
		Head:      cp.Head,
		CreatedAt: cp.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
	line := checkpointLine{checkpointBody: body, MAC: checkpointMAC(key, body)}
	b, err := json.Marshal(line)
	if err != nil {
		return Checkpoint{}, err
	}
	b = append(b, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Checkpoint{}, err
	}
	defer f.Close()
	if err := writeAll(f, b); err != nil {
		return Checkpoint{}, err
	}
	if err := f.Sync(); err != nil {
		return Checkpoint{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, body.CreatedAt)
	if err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{
		Version:   body.Version,
		Seq:       body.Seq,
		Head:      body.Head,
		CreatedAt: parsed,
		MAC:       line.MAC,
	}, nil
}

// loadCheckpoint returns the latest fully authenticated checkpoint.  A torn
// tail is discarded.  A complete line with a bad HMAC is reported as
// ErrCheckpointInvalid and is never used; callers may fall back to an earlier
// authenticated checkpoint but must isolate everything after the bad line.
func loadCheckpoint(path string, key []byte) (Checkpoint, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Checkpoint{}, false, nil
		}
		return Checkpoint{}, false, err
	}
	var latest Checkpoint
	found := false
	var lastValidEnd int64
	var problem error
	offset := int64(0)
	for offset < int64(len(data)) {
		rel := bytes.IndexByte(data[offset:], '\n')
		if rel < 0 {
			// Torn checkpoint line: crash artifact.  Do not trust it.
			break
		}
		line := data[offset : offset+int64(rel)]
		lineEnd := offset + int64(rel) + 1
		offset = lineEnd
		if len(bytes.TrimSpace(line)) == 0 {
			lastValidEnd = lineEnd
			continue
		}
		var parsed checkpointLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			problem = fmt.Errorf("%w: decode: %v", ErrCheckpointInvalid, err)
			break
		}
		if parsed.Version != 1 {
			problem = fmt.Errorf("%w: unsupported version %d", ErrCheckpointInvalid, parsed.Version)
			break
		}
		want := checkpointMAC(key, parsed.checkpointBody)
		if !hmac.Equal(parsed.MAC[:], want[:]) {
			problem = fmt.Errorf("%w: host HMAC mismatch", ErrCheckpointInvalid)
			break
		}
		created, err := time.Parse(time.RFC3339Nano, parsed.CreatedAt)
		if err != nil {
			problem = fmt.Errorf("%w: invalid creation time: %v", ErrCheckpointInvalid, err)
			break
		}
		if found && (parsed.Seq < latest.Seq || (parsed.Seq == latest.Seq && parsed.Head != latest.Head)) {
			problem = fmt.Errorf("%w: checkpoint sequence moved backwards or forked", ErrCheckpointInvalid)
			break
		}
		latest = Checkpoint{
			Version:   parsed.Version,
			Seq:       parsed.Seq,
			Head:      parsed.Head,
			CreatedAt: created,
			MAC:       parsed.MAC,
		}
		found = true
		lastValidEnd = lineEnd
	}
	if problem != nil {
		if lastValidEnd < int64(len(data)) {
			if err := os.Truncate(path, lastValidEnd); err != nil {
				return latest, found, errors.Join(problem, err)
			}
		}
		return latest, found, problem
	}
	if lastValidEnd < int64(len(data)) {
		if err := os.Truncate(path, lastValidEnd); err != nil {
			return latest, found, err
		}
	}
	return latest, found, nil
}
