package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// runInfo 描述一个已落槌（fsync 完成）的有序运行段。
type runInfo struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Count  int    `json:"count"`
}

// buildRuns 第一阶段：分块读入、内存稳定排序、写有序运行段。
// 每个运行段写完后 flush + fsync，并记录其 SHA-256 摘要。
func buildRuns(inputPath, workDir string, runSize int) ([]runInfo, error) {
	in, err := openTracked(inputPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer closeTracked(in)

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	var runs []runInfo
	var seq uint64
	for {
		batch := make([]record, 0, runSize)
		for len(batch) < runSize && scanner.Scan() {
			batch = append(batch, parseLine(scanner.Text(), seq))
			seq++
		}
		if len(batch) == 0 {
			break
		}
		// 稳定排序：相同键保持输入先后（即 seq 升序）。
		sort.SliceStable(batch, func(i, j int) bool { return batch[i].key < batch[j].key })
		ri, err := writeRun(workDir, len(runs), batch)
		if err != nil {
			return nil, err
		}
		runs = append(runs, ri)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

func writeRun(dir string, idx int, recs []record) (runInfo, error) {
	path := filepath.Join(dir, fmt.Sprintf("run-%05d.tmp", idx))
	f, err := openTracked(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return runInfo{}, err
	}
	h := sha256.New()
	w := bufio.NewWriter(io.MultiWriter(f, h))
	for _, r := range recs {
		if err := writeRecord(w, r); err != nil {
			closeTracked(f)
			return runInfo{}, err
		}
	}
	if err := w.Flush(); err != nil {
		closeTracked(f)
		return runInfo{}, err
	}
	if err := f.Sync(); err != nil {
		closeTracked(f)
		return runInfo{}, err
	}
	st, err := f.Stat()
	if err != nil {
		closeTracked(f)
		return runInfo{}, err
	}
	if err := closeTracked(f); err != nil {
		return runInfo{}, err
	}
	return runInfo{
		Path:   path,
		SHA256: hex.EncodeToString(h.Sum(nil)),
		Size:   st.Size(),
		Count:  len(recs),
	}, nil
}

// verifyRun 校验运行段摘要：恢复时不信任任何未核验的段。
func verifyRun(ri runInfo) error {
	f, err := openTracked(ri.Path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer closeTracked(f)
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != ri.Size {
		return fmt.Errorf("run %s: size %d != checkpoint %d", ri.Path, st.Size(), ri.Size)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != ri.SHA256 {
		return fmt.Errorf("run %s: digest mismatch", ri.Path)
	}
	return nil
}
