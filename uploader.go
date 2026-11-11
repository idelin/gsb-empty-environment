package cdcupload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Session tracks the resumable progress of one file upload. It is persisted
// after every successfully uploaded chunk, so a crash at any point leaves a
// consistent state that a later UploadFile call can continue from.
type Session struct {
	StatePath string       `json:"-"` // location of the persisted state
	FileID    string       `json:"file_id"`
	FileName  string       `json:"file_name"`
	Size      int64        `json:"size"`
	FileHash  string       `json:"file_hash"`
	Chunks    []ChunkEntry `json:"chunks"`
}

func sessionPath(stateDir, fileID string) string {
	return filepath.Join(stateDir, fileID+".session.json")
}

func loadSession(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSessionCorrupt, err)
	}
	s.StatePath = path
	return &s, nil
}

func (s *Session) persist() error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(s.StatePath, data, 0o600)
}

// UploadFile uploads filePath via uploader using content-defined chunking.
//
// stateDir holds per-file session state. If a valid session for the same file
// exists, the upload resumes: previously uploaded chunks are skipped and only
// missing chunks are transferred. The manifest is the very last operation and
// is committed atomically, so an interrupted upload is never visible as a
// complete file on the remote side.
func UploadFile(ctx context.Context, uploader Uploader, stateDir, filePath string, cfg ChunkConfig) (*Manifest, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return nil, err
	}

	// Whole-file digest up front: it identifies the upload and guards against
	// the source changing between resume runs. The streaming pass re-verifies it.
	fileHash, err := hashFile(filePath)
	if err != nil {
		return nil, err
	}
	// The file ID is stable per collector-visible file path so a modified file
	// resumes against the same session and gets rejected if its content moved.
	fileID := fileIDForPath(filePath)

	session, err := loadSession(sessionPath(stateDir, fileID))
	if err != nil {
		return nil, err
	}
	if session != nil {
		if session.Size != info.Size() || session.FileHash != fileHash {
			return nil, fmt.Errorf("%w: %s", ErrSourceChanged, filePath)
		}
	} else {
		session = &Session{
			StatePath: sessionPath(stateDir, fileID),
			FileID:    fileID,
			FileName:  filepath.Base(filePath),
			Size:      info.Size(),
			FileHash:  fileHash,
		}
		if err := session.persist(); err != nil {
			return nil, err
		}
	}

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	chunker, err := NewChunker(f, cfg)
	if err != nil {
		return nil, err
	}

	for {
		meta, payload, err := chunker.NextChunk()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		if int(meta.Index) < len(session.Chunks) {
			known := session.Chunks[meta.Index]
			if known.Offset != meta.Offset || known.Length != meta.Length ||
				known.Digest != hex.EncodeToString(meta.Digest) {
				return nil, fmt.Errorf("%w: re-chunked data diverges from session", ErrSourceChanged)
			}
			continue // already handled in an earlier run
		}

		digest := hex.EncodeToString(meta.Digest)
		exists, err := uploader.HasChunk(ctx, digest)
		if err != nil {
			return nil, err
		}
		if !exists {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if err := uploader.PutChunk(ctx, digest, payload); err != nil {
				return nil, err
			}
		}

		session.Chunks = append(session.Chunks, ChunkEntry{
			Index:  meta.Index,
			Offset: meta.Offset,
			Length: meta.Length,
			Digest: digest,
		})
		if err := session.persist(); err != nil {
			return nil, err
		}
	}

	wholeHash := hex.EncodeToString(chunker.FileSum())
	if wholeHash != fileHash {
		return nil, fmt.Errorf("%w: streaming digest mismatch", ErrSourceChanged)
	}

	manifest := &Manifest{
		FileID:    session.FileID,
		FileName:  session.FileName,
		Size:      session.Size,
		FileHash:  session.FileHash,
		ChunkSize: len(session.Chunks),
		Chunks:    session.Chunks,
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if err := uploader.CommitManifest(ctx, manifest); err != nil {
		return nil, err
	}
	return manifest, nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func fileIDForPath(path string) string {
	h := sha256.New()
	// Use the absolute path when available so distinct files sharing a base
	// name in different directories do not collide.
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	h.Write([]byte(abs))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Restore reassembles a committed manifest from the remote store into w and
// verifies every chunk digest plus the whole-file hash.
func Restore(ctx context.Context, store *RemoteStore, m *Manifest, w io.Writer) error {
	if err := m.Validate(); err != nil {
		return err
	}
	whole := sha256.New()
	for _, entry := range m.Chunks {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := store.ReadChunk(entry.Digest)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entry.Digest || len(data) != entry.Length {
			return fmt.Errorf("%w: chunk %d", ErrIntegrity, entry.Index)
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
		whole.Write(data)
	}
	if hex.EncodeToString(whole.Sum(nil)) != m.FileHash {
		return fmt.Errorf("%w: whole file digest", ErrIntegrity)
	}
	return nil
}
