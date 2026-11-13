package netsec

import (
	"crypto/sha256"
	"fmt"
	"io"
	"sort"
)

// Spec is the already-parsed model graph input. GraphDigest is the digest the
// host computed over the parsed graph; Freeze binds it into the weight
// manifest. Initializers are frozen in the order-independent set described by
// their tensor IDs and locations.
type Spec struct {
	Root         string
	GraphDigest  Digest
	Initializers []Initializer
	FileSystem   FileSystem
}

type frozenFile struct {
	location string
	handle   FileHandle
	entry    Entry
}

type resolvedRecord struct {
	normalizedInitializer
	file *frozenFile
}

type tensorEntry struct {
	normalizedInitializer
	file *frozenFile
}

// Freeze opens the model directory as a handle, opens every external
// initializer's file segment-by-segment without accepting symlinks, verifies
// identity, size, range, dtype/shape, overlap rules, and SHA-256 content, and
// returns a Model whose inference reads only from the resulting frozen
// handles. Any later change to a frozen file, short read, or manifest
// mismatch invalidates the entire instance.
func Freeze(spec Spec) (*Model, error) {
	if spec.Root == "" {
		return nil, fmt.Errorf("%w: empty model directory", ErrInvalidInitializer)
	}
	filesystem := spec.FileSystem
	if filesystem == nil {
		filesystem = DefaultFileSystem
	}

	records := make([]normalizedInitializer, 0, len(spec.Initializers))
	byID := make(map[string]normalizedInitializer, len(spec.Initializers))
	for _, initializer := range spec.Initializers {
		normalized, err := normalizeInitializer(initializer)
		if err != nil {
			return nil, err
		}
		if previous, ok := byID[normalized.TensorID]; ok {
			// A repeated tensor ID is only an alias of the same frozen
			// range, and only when both entries explicitly declare it
			// shared. Distinct tensors must have distinct IDs.
			if !previous.Shared || !normalized.Shared ||
				!initializersEqual(previous.Initializer, normalized.Initializer) {
				return nil, fmt.Errorf("%w: %q", ErrDuplicateTensorID, normalized.TensorID)
			}
		} else {
			byID[normalized.TensorID] = normalized
		}
		records = append(records, normalized)
	}

	root, err := filesystem.OpenModelDir(spec.Root)
	if err != nil {
		return nil, err
	}
	frozenFiles := make(map[string]*frozenFile)
	closeAll := func() {
		for _, file := range frozenFiles {
			_ = file.handle.Close()
		}
		_ = root.Close()
	}

	locations := make([]string, 0, len(records))
	seenLocations := make(map[string]struct{}, len(records))
	for _, record := range records {
		if _, ok := seenLocations[record.Location]; ok {
			continue
		}
		seenLocations[record.Location] = struct{}{}
		locations = append(locations, record.Location)
	}
	sort.Strings(locations)

	for _, location := range locations {
		handle, entry, err := openWeightFile(root, location)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("netsec: freeze %q: %w", location, err)
		}
		frozenFiles[location] = &frozenFile{
			location: location,
			handle:   handle,
			entry:    entry,
		}
	}

	resolved := make([]resolvedRecord, 0, len(records))
	for _, record := range records {
		file := frozenFiles[record.Location]
		if err := checkedRangeInFile(record.Offset, record.Length, file.entry.Size); err != nil {
			closeAll()
			return nil, fmt.Errorf("netsec: freeze %q: %w", record.TensorID, err)
		}
		resolved = append(resolved, resolvedRecord{
			normalizedInitializer: record,
			file:                  file,
		})
	}
	if err := checkOverlaps(resolved); err != nil {
		closeAll()
		return nil, err
	}

	for _, record := range resolved {
		if err := verifyFrozenFile(record.file); err != nil {
			closeAll()
			return nil, fmt.Errorf("netsec: freeze %q: %w", record.TensorID, err)
		}
		digest, err := hashRange(record.file.handle, record.Offset, record.Length)
		if err != nil {
			closeAll()
			return nil, fmt.Errorf("netsec: freeze %q: %w", record.TensorID, err)
		}
		if digest != record.SHA256 {
			closeAll()
			return nil, fmt.Errorf(
				"%w: tensor %q: got %s, want %s",
				ErrDigestMismatch, record.TensorID, digest, record.SHA256,
			)
		}
		if err := verifyFrozenFile(record.file); err != nil {
			closeAll()
			return nil, fmt.Errorf("netsec: freeze %q: %w", record.TensorID, err)
		}
	}

	manifest, err := buildManifest(spec.GraphDigest, frozenFiles, byID)
	if err != nil {
		closeAll()
		return nil, err
	}
	tensors := make(map[string]*tensorEntry, len(byID))
	for id, normalized := range byID {
		tensors[id] = &tensorEntry{
			normalizedInitializer: normalized,
			file:                  frozenFiles[normalized.Location],
		}
	}

	return &Model{
		root:        root,
		files:       frozenFiles,
		tensors:     tensors,
		graphDigest: spec.GraphDigest,
		manifest:    manifest,
	}, nil
}

func openWeightFile(root DirHandle, location string) (FileHandle, Entry, error) {
	parts, err := splitLocation(location)
	if err != nil {
		return nil, Entry{}, err
	}

	current := root
	var openedDirs []DirHandle
	defer func() {
		for _, dir := range openedDirs {
			_ = dir.Close()
		}
	}()

	for index, part := range parts {
		before, err := current.Lstat(part)
		if err != nil {
			return nil, Entry{}, fmt.Errorf("netsec: lstat %q: %w", part, err)
		}
		if before.symlink() {
			return nil, Entry{}, fmt.Errorf("%w: %q", ErrSymlink, part)
		}

		last := index == len(parts)-1
		if last {
			if !before.regular() {
				return nil, Entry{}, fmt.Errorf("%w: %q", ErrNotRegular, part)
			}
			handle, err := current.OpenFile(part)
			if err != nil {
				return nil, Entry{}, fmt.Errorf("netsec: open %q: %w", part, err)
			}
			after, err := handle.Stat()
			if err != nil {
				handle.Close()
				return nil, Entry{}, fmt.Errorf("netsec: stat opened %q: %w", part, err)
			}
			if !after.regular() || !sameIdentity(before.Identity, after.Identity) {
				handle.Close()
				return nil, Entry{}, fmt.Errorf("%w: %q", ErrChangedDuringOpen, part)
			}
			if after.Identity.Links > 1 {
				handle.Close()
				return nil, Entry{}, fmt.Errorf("%w: %q has %d links", ErrHardlink, part, after.Identity.Links)
			}
			return handle, after, nil
		}

		if !before.dir() {
			return nil, Entry{}, fmt.Errorf("%w: %q", ErrNotDirectory, part)
		}
		next, err := current.OpenDir(part)
		if err != nil {
			return nil, Entry{}, fmt.Errorf("netsec: open directory %q: %w", part, err)
		}
		after, err := next.Stat()
		if err != nil {
			next.Close()
			return nil, Entry{}, fmt.Errorf("netsec: stat opened directory %q: %w", part, err)
		}
		if !after.dir() || !sameIdentity(before.Identity, after.Identity) {
			next.Close()
			return nil, Entry{}, fmt.Errorf("%w: directory %q", ErrChangedDuringOpen, part)
		}
		openedDirs = append(openedDirs, next)
		current = next
	}
	return nil, Entry{}, fmt.Errorf("%w: empty location", ErrInvalidLocation)
}

func verifyFrozenFile(file *frozenFile) error {
	if file == nil || file.handle == nil {
		return fmt.Errorf("%w: missing frozen file", ErrModelInvalid)
	}
	current, err := file.handle.Stat()
	if err != nil {
		return fmt.Errorf("netsec: stat frozen %q: %w", file.location, err)
	}
	if !current.regular() {
		return fmt.Errorf("%w: %q is no longer regular", ErrModelInvalid, file.location)
	}
	if current.Identity.Links > 1 {
		return fmt.Errorf("%w: %q has %d links", ErrHardlink, file.location, current.Identity.Links)
	}
	if !sameFrozenIdentity(current.Identity, file.entry.Identity) {
		return fmt.Errorf("%w: file %q changed", ErrModelInvalid, file.location)
	}
	return nil
}

func checkOverlaps(records []resolvedRecord) error {
	groups := make(map[string][]resolvedRecord)
	for _, record := range records {
		key := identityKey(record.file.entry.Identity)
		groups[key] = append(groups[key], record)
	}
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			if group[i].Offset == group[j].Offset {
				return group[i].Length < group[j].Length
			}
			return group[i].Offset < group[j].Offset
		})
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group) && group[j].Offset < group[i].end; j++ {
				if !rangesOverlap(group[i].normalizedInitializer, group[j].normalizedInitializer) ||
					overlapAllowed(group[i].normalizedInitializer, group[j].normalizedInitializer) {
					continue
				}
				return fmt.Errorf(
					"%w: tensors %q and %q overlap in %q",
					ErrOverlappingRanges,
					group[i].TensorID, group[j].TensorID, group[i].Location,
				)
			}
		}
	}
	return nil
}

func buildManifest(graphDigest Digest, files map[string]*frozenFile, byID map[string]normalizedInitializer) (WeightManifest, error) {
	manifest := WeightManifest{GraphDigest: graphDigest}
	locations := make([]string, 0, len(files))
	for location := range files {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	for _, location := range locations {
		file := files[location]
		manifest.Files = append(manifest.Files, ManifestFile{
			Location: location,
			Size:     file.entry.Size,
			Identity: file.entry.Identity,
		})
	}

	tensors := make([]ManifestTensor, 0, len(byID))
	for _, normalized := range byID {
		file, ok := files[normalized.Location]
		if !ok {
			return WeightManifest{}, fmt.Errorf("%w: missing file for %q", ErrInvalidInitializer, normalized.TensorID)
		}
		tensors = append(tensors, ManifestTensor{
			TensorID: normalized.TensorID,
			Location: normalized.Location,
			Offset:   normalized.Offset,
			Length:   normalized.Length,
			SHA256:   normalized.SHA256,
			Dtype:    normalized.Dtype,
			Shape:    append([]int64(nil), normalized.Shape...),
			Shared:   normalized.Shared,
			Identity: file.entry.Identity,
		})
	}
	sort.Slice(tensors, func(i, j int) bool {
		if tensors[i].TensorID != tensors[j].TensorID {
			return tensors[i].TensorID < tensors[j].TensorID
		}
		if tensors[i].Location != tensors[j].Location {
			return tensors[i].Location < tensors[j].Location
		}
		return tensors[i].Offset < tensors[j].Offset
	})
	manifest.Tensors = tensors
	return manifest, nil
}

func hashRange(file FileHandle, offset, length int64) (Digest, error) {
	hasher := sha256.New()
	remaining := length
	const bufferSize = 64 * 1024
	buffer := make([]byte, bufferSize)
	for remaining > 0 {
		chunk := int64(bufferSize)
		if remaining < chunk {
			chunk = remaining
		}
		if err := readAtExact(file, buffer[:int(chunk)], offset); err != nil {
			return Digest{}, err
		}
		if _, err := hasher.Write(buffer[:int(chunk)]); err != nil {
			return Digest{}, err
		}
		offset += chunk
		remaining -= chunk
	}
	var digest Digest
	copy(digest[:], hasher.Sum(nil))
	return digest, nil
}

func readAtExact(file FileHandle, buffer []byte, offset int64) error {
	read := 0
	for read < len(buffer) {
		want := len(buffer) - read
		n, err := file.ReadAt(buffer[read:], offset+int64(read))
		if n < 0 || n > want {
			return fmt.Errorf("netsec: invalid ReadAt result %d for %d bytes", n, want)
		}
		read += n
		if read == len(buffer) {
			return nil
		}
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("%w: read %d of %d bytes at offset %d", ErrShortRead, read, len(buffer), offset)
			}
			return fmt.Errorf("netsec: read at offset %d: %w", offset+int64(read), err)
		}
		if n == 0 {
			return fmt.Errorf("%w: no progress at offset %d", ErrShortRead, offset+int64(read))
		}
	}
	return nil
}
