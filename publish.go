package auditjoin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// publishLocked 将条目写入存储。首次发布版本为 1；带新片段的补正沿用原键并版本 +1；
// 无新片段的重放走存储幂等路径，不递增版本、不产生重复事件。
func (a *Assembler) publishLocked(ctx context.Context, entry *openEntry, complete bool, now time.Time, reason string) (Record, error) {
	payload := joinPayload(entry)

	var version uint64
	switch {
	case entry.PublishedVersion == 0:
		version = 1
	case entry.DirtySincePublish || (!entry.Complete && complete):
		version = entry.PublishedVersion + 1
	default:
		version = entry.PublishedVersion
	}

	publishedAt := now
	if !entry.PublishedAt.IsZero() {
		publishedAt = entry.PublishedAt
	}
	rec := Record{
		Key:         entry.Key,
		Cycle:       entry.Cycle,
		Version:     version,
		Complete:    complete,
		Payload:     payload,
		PublishedAt: publishedAt,
		UpdatedAt:   now,
	}

	stored, changed, err := a.store.Put(ctx, entry.Key, entry.Cycle, rec)
	if err != nil {
		if errors.Is(err, ErrConflict) {
			a.stats.StoreConflicts++
		}
		return Record{}, err
	}

	firstPublish := entry.PublishedVersion == 0
	correction := !firstPublish && changed

	entry.PublishedVersion = stored.Version
	entry.PublishedAt = stored.PublishedAt
	entry.Complete = stored.Complete
	entry.DirtySincePublish = false

	switch {
	case !changed:
		a.stats.IdempotentPuts++
	case correction:
		a.stats.Corrections++
	case reason == "timeout":
		a.stats.TimeoutPublished++
	case reason == "watermark":
		a.stats.WatermarkPublished++
	default:
		a.stats.CompletePublished++
	}

	if changed {
		entry.Deadline = a.deadline(now)
		if complete {
			entry.Deadline = time.Time{}
		}
	}
	return stored, nil
}

func joinPayload(entry *openEntry) []byte {
	var buf bytes.Buffer
	for i, fid := range entry.Order {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.Write(entry.Frags[fid].Payload)
	}
	return buf.Bytes()
}

func rebuildEntry(rec Record) *openEntry {
	return &openEntry{
		Key:              rec.Key,
		Cycle:            rec.Cycle,
		Frags:            make(map[FragmentID]Fragment),
		PublishedVersion: rec.Version,
		PublishedAt:      rec.PublishedAt,
		Complete:         rec.Complete,
		Deadline:         time.Time{},
	}
}

func validateFragment(f Fragment) error {
	if f.NodeID == "" || f.Generation == "" || f.ShardID == "" || f.FragmentID == "" {
		return fmt.Errorf("%w: node, generation, shard and fragment id are required", ErrInvalidFragment)
	}
	return nil
}

// isWraparound 判断从 prev 到 cur 的跳变是否为跨过 0 的序号回绕：
// 仅当 prev 处于高半区、cur 处于低半区时成立；普通乱序（同半区内回退）不算回绕。
func isWraparound(prev, cur, modulus uint64) bool {
	if modulus < 2 {
		return false
	}
	half := modulus / 2
	return prev >= half && cur < half
}

func compositeKeyString(ck compositeKey) string {
	return fmt.Sprintf("%s|c%d|s%d", ck.Key.String(), ck.Cycle, ck.Key.Seq)
}

func (a *Assembler) persistLocked() error {
	if a.statePath == "" {
		return nil
	}
	snap := assemblerSnapshot{
		Open:       make([]openEntry, 0, len(a.open)),
		Rings:      make([]ringDTO, 0, len(a.rings)),
		Watermarks: make([]watermarkDTO, 0, len(a.watermarks)),
		Stats:      a.stats,
	}
	for _, e := range a.open {
		snap.Open = append(snap.Open, *e)
	}
	for sk, ring := range a.rings {
		snap.Rings = append(snap.Rings, ringDTO{Stream: sk, Ring: ring})
	}
	for sid, wm := range a.watermarks {
		snap.Watermarks = append(snap.Watermarks, watermarkDTO{Shard: sid, Watermark: wm})
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("auditjoin: encode state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(a.statePath), 0o755); err != nil {
		return fmt.Errorf("auditjoin: mkdir state: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(a.statePath), ".auditjoin-state-*")
	if err != nil {
		return fmt.Errorf("auditjoin: create temp state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("auditjoin: write state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("auditjoin: fsync state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, a.statePath); err != nil {
		return fmt.Errorf("auditjoin: rename state: %w", err)
	}
	return nil
}

func (a *Assembler) loadLocked() error {
	data, err := os.ReadFile(a.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("auditjoin: read state %q: %w", a.statePath, err)
	}
	if len(data) == 0 {
		return nil
	}
	var snap assemblerSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("auditjoin: decode state %q: %w", a.statePath, err)
	}
	for i := range snap.Open {
		e := snap.Open[i]
		if e.Frags == nil {
			e.Frags = make(map[FragmentID]Fragment)
		}
		entry := e
		a.open[compositeKey{Key: entry.Key, Cycle: entry.Cycle}] = &entry
	}
	for _, dto := range snap.Rings {
		a.rings[dto.Stream] = dto.Ring
	}
	for _, dto := range snap.Watermarks {
		a.watermarks[dto.Shard] = dto.Watermark
	}
	a.stats = snap.Stats
	return nil
}
