package idempotentjournal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	journalName       = "journal.log"
	tempJournalName   = "journal.compact.tmp"
	journalDirPerm    = 0700
	journalFilePerm   = 0600
	maxIDLength       = 1024
	maxPayloadLength  = 16 << 20
	defaultMaxPayload = 1 << 20
	frameHeaderSize   = 20
	maxFrameLength    = 64 << 20

	frameData uint8 = 1
	frameMeta uint8 = 2
)

var (
	ErrConflict        = errors.New("idempotency key already exists with different content")
	ErrUnknownID       = errors.New("unknown idempotency key")
	ErrOffsetReclaimed = errors.New("offset has been reclaimed")
	ErrUnknownOffset   = errors.New("unknown offset")
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClosed          = errors.New("store is closed")
	ErrInjectedFault   = errors.New("injected fault")
)

type Retention struct {
	MinAge        time.Duration
	RetainAtLeast int
}

type Options struct {
	Retention    Retention
	Now          func() time.Time
	MaxPayload   int
	Faults       FaultHooks
	FileMode     os.FileMode
	DirMode      os.FileMode
}

type FaultHooks struct {
	BeforeSync        func() error
	AfterSync         func() error
	AfterTempSync     func() error
	AfterRename       func() error
	AfterDirectorySync func() error
}

type Record struct {
	ID        string
	Offset    uint64
	Published time.Time
	Payload   []byte
	SHA256    [sha256.Size]byte
}

type ReclaimResult struct {
	ReclaimedThrough uint64
	RetainedFrom     uint64
	RemovedRecords   int
	Compacted        bool
}

type dataPayload struct {
	Version   uint8
	ID        string
	Timestamp int64
	Offset    uint64
	Hash      [sha256.Size]byte
	Payload   []byte
}

type metadataPayload struct {
	Version          uint8
	NextOffset       uint64
	ReclaimedThrough uint64
}

type entry struct {
	rec        Record
	frameStart int64
}

type Store struct {
	dir           string
	file          *os.File
	mu            sync.Mutex
	entries       []*entry
	byID          map[string]*entry
	nextOffset    uint64
	reclaimedHigh uint64
	opts          Options
	closed        bool
}

func Open(ctx context.Context, dir string, opts Options) (*Store, error) {
	if opts.Retention.MinAge < 0 {
		return nil, fmt.Errorf("%w: negative retention duration", ErrInvalidArgument)
	}
	if opts.Retention.RetainAtLeast < 0 {
		return nil, fmt.Errorf("%w: negative retain count", ErrInvalidArgument)
	}
	if opts.MaxPayload < 0 {
		return nil, fmt.Errorf("%w: negative payload limit", ErrInvalidArgument)
	}
	if opts.MaxPayload == 0 {
		opts.MaxPayload = defaultMaxPayload
	}
	if opts.MaxPayload > maxPayloadLength {
		return nil, fmt.Errorf("%w: payload limit too large", ErrInvalidArgument)
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	if opts.DirMode == 0 {
		opts.DirMode = journalDirPerm
	}
	if opts.FileMode == 0 {
		opts.FileMode = journalFilePerm
	}

	if dir == "" {
		return nil, fmt.Errorf("%w: empty directory", ErrInvalidArgument)
	}
	if err := os.MkdirAll(dir, opts.DirMode); err != nil {
		return nil, err
	}

	created := false
	path := filepath.Join(dir, journalName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, opts.FileMode)
	if err != nil {
		return nil, err
	}
	if info, statErr := file.Stat(); statErr == nil && info.Size() == 0 {
		created = true
	}
	_ = ctx

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return nil, err
	}
	if created {
		if err := syncDir(dir); err != nil {
			_ = file.Close()
			return nil, err
		}
	}

	store := &Store{dir: dir, file: file, byID: map[string]*entry{}, opts: opts}
	if err := store.load(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}
