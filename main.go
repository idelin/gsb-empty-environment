package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// crashFunc 模拟进程崩溃：CLI 模式直接退出（不 flush、不关闭），
// 自检模式替换为 panic 以便进程内恢复。
var crashFunc = func() { os.Exit(2) }

func usage() {
	fmt.Fprintf(os.Stderr, `用法:
  extsort sort -input F -output F -workdir D [-run-size N] [-checkpoint-every N] [-fail-after N]
  extsort dump -input F
  extsort selftest [-workdir D] [-records N] [-run-size N] [-checkpoint-every N] [-seed N]
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "sort":
		err = cmdSort(os.Args[2:])
	case "dump":
		err = cmdDump(os.Args[2:])
	case "selftest":
		err = cmdSelfTest(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func cmdSort(args []string) error {
	fs := flag.NewFlagSet("sort", flag.ContinueOnError)
	input := fs.String("input", "", "输入文本文件（每行 key\\tvalue）")
	output := fs.String("output", "", "排序输出文件")
	workdir := fs.String("workdir", "", "运行段与检查点目录")
	runSize := fs.Int("run-size", 100000, "每个运行段的记录数")
	ckEvery := fs.Uint64("checkpoint-every", 50000, "每多少条记录做一次检查点")
	failAfter := fs.Uint64("fail-after", 0, "发出 N 条记录后模拟崩溃（测试用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" || *output == "" || *workdir == "" {
		return fmt.Errorf("-input, -output and -workdir are required")
	}
	if err := os.MkdirAll(*workdir, 0o755); err != nil {
		return err
	}
	absOut, err := filepath.Abs(*output)
	if err != nil {
		return err
	}

	if !checkpointExists(*workdir) {
		runs, err := buildRuns(*input, *workdir, *runSize)
		if err != nil {
			return err
		}
		fmt.Printf("phase 1: wrote %d sorted runs\n", len(runs))
		if err := saveCheckpoint(*workdir, initialCheckpoint(runs, absOut)); err != nil {
			return err
		}
	} else {
		fmt.Println("checkpoint found: resuming merge")
	}

	m, err := newMerger(*workdir, *ckEvery, *failAfter)
	if err != nil {
		return err
	}
	defer m.Close()
	if err := m.run(); err != nil {
		return err
	}
	fmt.Printf("done: %d/%d records -> %s (peak open files: %d)\n",
		m.emitted, m.totalRecords(), m.ck.OutPath, peakOpenFiles())
	return nil
}

// cmdDump 把二进制输出解码为 "seq key value" 文本行，便于核对。
func cmdDump(args []string) error {
	fs := flag.NewFlagSet("dump", flag.ContinueOnError)
	input := fs.String("input", "", "二进制输出文件")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *input == "" {
		return fmt.Errorf("-input is required")
	}
	f, err := os.Open(*input)
	if err != nil {
		return err
	}
	defer f.Close()
	br := bufio.NewReader(f)
	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()
	for {
		rec, err := readRecord(br)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "%d\t%s\t%s\n", rec.seq, rec.key, rec.val)
	}
}
