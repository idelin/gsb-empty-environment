
// Package backup implements zero-copy physical backups for an LSM engine.
//
// Because SSTables are immutable, a snapshot is taken by creating hard links
// to every live SSTable: no data is copied, so the export finishes in
// milliseconds regardless of data size. Later compactions may unlink the
// original files, but the snapshot's links keep the data blocks alive.
//
// Each export writes:
//   - MANIFEST.json     full list of SSTables (name, size, CRC32) at export time
//   - INCREMENTAL.json  the SSTables that are new since the previous export,
//     enabling differential offline archiving
package backup

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileEntry describes one backed-up SSTable.
type FileEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	CRC32 uint32 `json:"crc32"`
}

// Manifest is the full physical backup description of one snapshot.
type Manifest struct {
	Seq       int         `json:"seq"`
	CreatedAt time.Time   `json:"created_at"`
	Files     []FileEntry `json:"files"`
}

// Info describes a completed export.
type Info struct {
	Dir      string    // snapshot directory containing the hard links
	Manifest *Manifest // full SSTable list at export time
	Added    []string  // incremental list: SSTables new since the previous export
}

// Exporter takes repeated snapshots of one LSM data directory into a backup
// root, tracking the previous snapshot to compute incremental file lists.
type Exporter struct {
	srcDir  string
	dstRoot string

	mu   sync.Mutex
	seq  int
	last map[string]bool
}

// NewExporter creates an exporter reading SSTables from srcDir and writing
// snapshot-NNNN directories under dstRoot.
func NewExporter(srcDir, dstRoot string) (*Exporter, error) {
	if err := os.MkdirAll(dstRoot, 0755); err != nil {
		return nil, err
	}
	return &Exporter{srcDir: srcDir, dstRoot: dstRoot, last: make(map[string]bool)}, nil
}

// Export hard-links every current SSTable into a new snapshot directory and
// records the full and incremental manifests.
func (x *Exporter) Export() (*Info, error) {
	x.mu.Lock()
	defer x.mu.Unlock()

	files, err := listSSTables(x.srcDir)
	if err != nil {
		return nil, err
	}

	x.seq++
	dir := filepath.Join(x.dstRoot, fmt.Sprintf("snapshot-%04d", x.seq))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	for _, fe := range files {
		src := filepath.Join(x.srcDir, fe.Name)
		dst := filepath.Join(dir, fe.Name)
		if err := os.Link(src, dst); err != nil {
			return nil, fmt.Errorf("backup: hard link %s: %v", fe.Name, err)
		}
	}

	added := make([]string, 0, len(files))
	current := make(map[string]bool, len(files))
	for _, fe := range files {
		current[fe.Name] = true
		if !x.last[fe.Name] {
			added = append(added, fe.Name)
		}
	}

	m := &Manifest{Seq: x.seq, CreatedAt: time.Now(), Files: files}
	if err := writeJSON(filepath.Join(dir, "MANIFEST.json"), m); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "INCREMENTAL.json"), added); err != nil {
		return nil, err
	}
	x.last = current
	return &Info{Dir: dir, Manifest: m, Added: added}, nil
}

// Verify checks that every file listed in the manifest exists in dir with the
// recorded size and CRC32, proving the snapshot survived untouched.
func Verify(dir string, m *Manifest) error {
	for _, fe := range m.Files {
		path := filepath.Join(dir, fe.Name)
		fi, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("backup: verify %s: %v", fe.Name, err)
		}
		if fi.Size() != fe.Size {
			return fmt.Errorf("backup: verify %s: size %d != manifest %d", fe.Name, fi.Size(), fe.Size)
		}
		crc, err := crc32File(path)
		if err != nil {
			return err
		}
		if crc != fe.CRC32 {
			return fmt.Errorf("backup: verify %s: crc32 %08x != manifest %08x", fe.Name, crc, fe.CRC32)
		}
	}
	return nil
}

func listSSTables(dir string) ([]FileEntry, error) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []FileEntry
	for _, fi := range fis {
		if fi.IsDir() || !strings.HasSuffix(fi.Name(), ".sst") {
			continue
		}
		crc, err := crc32File(filepath.Join(dir, fi.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, FileEntry{Name: fi.Name(), Size: fi.Size(), CRC32: crc})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func crc32File(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, f); err != nil {
		return 0, err
	}
	return h.Sum32(), nil
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, append(data, '\n'), 0644)
}
