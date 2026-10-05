package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
)

// errInjectedCrash 是自检模式下模拟崩溃的 panic 值。
var errInjectedCrash = errors.New("injected crash")

// genInput 生成含大量重复键的输入，以检验稳定性。
func genInput(path string, n int, rng *rand.Rand) error {
	f, err := openTracked(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for i := 0; i < n; i++ {
		fmt.Fprintf(w, "key-%04d\tpayload-%d-%d\n", rng.Intn(500), i, rng.Int63())
	}
	if err := w.Flush(); err != nil {
		closeTracked(f)
		return err
	}
	return closeTracked(f)
}

// expectedHash 用一次内存稳定排序算出基准输出的摘要。
func expectedHash(inputPath string) (string, int, error) {
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return "", 0, err
	}
	var recs []record
	var seq uint64
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		recs = append(recs, parseLine(sc.Text(), seq))
		seq++
	}
	if err := sc.Err(); err != nil {
		return "", 0, err
	}
	sort.SliceStable(recs, func(i, j int) bool { return recs[i].key < recs[j].key })
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	for _, r := range recs {
		if err := writeRecord(w, r); err != nil {
			return "", 0, err
		}
	}
	if err := w.Flush(); err != nil {
		return "", 0, err
	}
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:]), len(recs), nil
}

func hashFile(path string) (string, error) {
	f, err := openTracked(path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer closeTracked(f)
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// faultPoints 生成故障切点：每个检查点边界的前后各一点，另加首尾与若干中点。
func faultPoints(total int, ckEvery uint64) []uint64 {
	seen := map[uint64]bool{}
	var pts []uint64
	add := func(v int64) {
		if v < 0 || v > int64(total) || seen[uint64(v)] {
			return
		}
		seen[uint64(v)] = true
		pts = append(pts, uint64(v))
	}
	add(0)
	add(1)
	for b := int64(ckEvery); b < int64(total); b += int64(ckEvery) {
		add(b - 1)
		add(b)
		add(b + 1)
		add(b - int64(ckEvery)/2)
	}
	add(int64(total) - 1)
	add(int64(total))
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	return pts
}

// attempt 执行一次归并尝试，捕获注入的崩溃并清理资源。
func attempt(m *merger) (crashed bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if r == errInjectedCrash {
				crashed = true
			} else {
				err = fmt.Errorf("unexpected panic: %v", r)
			}
		}
		m.Close()
	}()
	err = m.run()
	return
}

func cmdSelfTest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	workdir := fs.String("workdir", "selftest-data", "工作目录")
	records := fs.Int("records", 20000, "输入记录数")
	runSize := fs.Int("run-size", 1500, "每个运行段的记录数")
	ckEvery := fs.Uint64("checkpoint-every", 1000, "每多少条记录做一次检查点")
	seed := fs.Int64("seed", 42, "随机种子")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// 自检模式下崩溃以 panic 模拟（进程内恢复），CLI sort 模式用 os.Exit。
	crashFunc = func() { panic(errInjectedCrash) }

	if err := os.RemoveAll(*workdir); err != nil {
		return err
	}
	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		return err
	}
	inputPath := filepath.Join(*workdir, "input.txt")
	outPath := filepath.Join(*workdir, "output.bin")

	rng := rand.New(rand.NewSource(*seed))
	if err := genInput(inputPath, *records, rng); err != nil {
		return err
	}
	expHash, total, err := expectedHash(inputPath)
	if err != nil {
		return err
	}
	runs, err := buildRuns(inputPath, *workdir, *runSize)
	if err != nil {
		return err
	}
	fmt.Printf("input: %d records, %d runs, checkpoint every %d\n", total, len(runs), *ckEvery)

	pts := faultPoints(total, *ckEvery)
	for _, f := range pts {
		// 每个故障切点从零开始：清掉输出与检查点，重建初始检查点。
		os.Remove(outPath)
		os.Remove(checkpointPath(*workdir))
		if err := saveCheckpoint(*workdir, initialCheckpoint(runs, outPath)); err != nil {
			return err
		}
		failAfter := f // 仅首次尝试注入故障
		restarts := 0
		for {
			m, err := newMerger(*workdir, *ckEvery, failAfter)
			if err != nil {
				return fmt.Errorf("fault %d: resume: %v", f, err)
			}
			crashed, err := attempt(m)
			if err != nil {
				return fmt.Errorf("fault %d: %v", f, err)
			}
			failAfter = 0
			if !crashed {
				break
			}
			restarts++
		}
		got, err := hashFile(outPath)
		if err != nil {
			return err
		}
		if got != expHash {
			return fmt.Errorf("fault %d: output digest mismatch (got %s, want %s)", f, got, expHash)
		}
		fmt.Printf("fault point %6d: recovered after %d restart(s), output matches\n", f, restarts)
	}
	fmt.Printf("OK: %d fault points verified against in-memory stable sort; peak open files = %d\n",
		len(pts), peakOpenFiles())
	return nil
}
