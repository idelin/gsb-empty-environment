package gitaccess

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// sha1Sum is a small helper kept next to the storage code so callers do not
// need to import crypto/sha1 directly.
func sha1Sum(data []byte) ObjectID {
	return ObjectID(sha1.Sum(data))
}

// looseObject is a zlib-compressed, framed object as stored in .git/objects.
type looseObject struct {
	typ ObjectType
	// compressed is the zlib stream of "<type> <size>\0<content>".
	compressed []byte
}

// ObjectDatabase is the artifact service's shared object storage. It holds
// loose objects and registered packs and materializes objects on demand.
//
// The database itself performs no authorization: it only guarantees that any
// returned object matches its content digest. Capability enforcement lives in
// Service and Capability.
type ObjectDatabase struct {
	mu    sync.RWMutex
	loose map[ObjectID]looseObject
	packs []*Pack
}

// NewObjectDatabase creates an empty database.
func NewObjectDatabase() *ObjectDatabase {
	return &ObjectDatabase{loose: make(map[ObjectID]looseObject)}
}

// AddLoose stores an object in loose form. The object is digest checked
// before storage, so a corrupt or mislabeled object is rejected up front.
func (db *ObjectDatabase) AddLoose(obj *Object) (ObjectID, error) {
	id := hashObject(obj.Type, obj.Content)

	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(frameObject(obj.Type, obj.Content)); err != nil {
		return ObjectID{}, err
	}
	if err := zw.Close(); err != nil {
		return ObjectID{}, err
	}

	db.mu.Lock()
	defer db.mu.Unlock()
	db.loose[id] = looseObject{typ: obj.Type, compressed: buf.Bytes()}
	return id, nil
}

// RegisterPack makes all objects of an already verified pack available.
func (db *ObjectDatabase) RegisterPack(p *Pack) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.packs = append(db.packs, p)
}

// Has reports whether the database can locate the object, without
// materializing it.
func (db *ObjectDatabase) Has(id ObjectID) bool {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if _, ok := db.loose[id]; ok {
		return true
	}
	for _, p := range db.packs {
		if _, ok := p.byID[id]; ok {
			return true
		}
	}
	return false
}

// LoadVerified materializes an object and never returns content unless the
// Git SHA-1 content digest of the result equals id. Loose objects are checked
// on every load; pack objects are checked at the end of delta reconstruction.
func (db *ObjectDatabase) LoadVerified(ctx context.Context, id ObjectID) (*Object, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return db.load(ctx, id, nil)
}

// load implements lookup. excludePack is skipped while searching packs; it
// prevents infinite recursion when a thin pack resolves a REF_DELTA base
// outside itself.
func (db *ObjectDatabase) load(ctx context.Context, id ObjectID, excludePack *Pack) (*Object, error) {
	db.mu.RLock()
	loose, hasLoose := db.loose[id]
	packs := make([]*Pack, len(db.packs))
	copy(packs, db.packs)
	db.mu.RUnlock()

	if hasLoose {
		obj, err := inflateLoose(loose)
		if err != nil {
			return nil, err
		}
		if err := verifyDigest(obj, id); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return obj, nil
	}

	// Deterministic order keeps error behavior and resolution stable.
	sort.SliceStable(packs, func(i, j int) bool {
		return packs[i].entries[0].offset < packs[j].entries[0].offset
	})
	for _, p := range packs {
		if p == excludePack {
			continue
		}
		entry, ok := p.byID[id]
		if !ok {
			continue
		}
		memo := make(map[int64]*Object)
		return p.materialize(ctx, db, entry, 0, memo)
	}
	return nil, fmt.Errorf("%w: %s", ErrObjectNotFound, id)
}

func inflateLoose(loose looseObject) (*Object, error) {
	zr, err := zlib.NewReader(bytes.NewReader(loose.compressed))
	if err != nil {
		return nil, fmt.Errorf("%w: cannot open loose object: %v", ErrChecksumMismatch, err)
	}
	framed, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot inflate loose object: %v", ErrChecksumMismatch, err)
	}
	if err := zr.Close(); err != nil {
		return nil, err
	}

	null := bytes.IndexByte(framed, 0)
	if null < 0 {
		return nil, fmt.Errorf("%w: loose object missing framing", ErrChecksumMismatch)
	}
	header := strings.Fields(string(framed[:null]))
	if len(header) != 2 {
		return nil, fmt.Errorf("%w: bad loose object header", ErrChecksumMismatch)
	}
	typeName := header[0]
	size, err := strconv.Atoi(header[1])
	if err != nil {
		return nil, fmt.Errorf("%w: bad loose object header: %v", ErrChecksumMismatch, err)
	}
	content := framed[null+1:]
	if len(content) != size {
		return nil, fmt.Errorf("%w: loose object size %d != header %d", ErrChecksumMismatch, len(content), size)
	}

	var typ ObjectType
	switch typeName {
	case "commit":
		typ = CommitObject
	case "tree":
		typ = TreeObject
	case "blob":
		typ = BlobObject
	case "tag":
		typ = TagObject
	default:
		return nil, fmt.Errorf("%w: unknown loose object type %q", ErrChecksumMismatch, typeName)
	}
	if typ != loose.typ {
		return nil, fmt.Errorf("%w: loose object type mismatch", ErrChecksumMismatch)
	}
	return &Object{Type: typ, Content: content}, nil
}
