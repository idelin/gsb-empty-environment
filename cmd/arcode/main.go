// arcode 是算术编码的命令行样例。
//
//	arcode encode <输入文件> <输出文件>
//	arcode decode <输入文件> <输出文件> [输出预算字节数]
package main

import (
	"fmt"
	"os"
	"strconv"

	"arcode"
)

const defaultBudget = 1 << 30 // 1 GiB

func usage() {
	fmt.Fprintf(os.Stderr, "用法:\n")
	fmt.Fprintf(os.Stderr, "  arcode encode <输入文件> <输出文件>\n")
	fmt.Fprintf(os.Stderr, "  arcode decode <输入文件> <输出文件> [输出预算字节数, 默认 %d]\n", defaultBudget)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 4 {
		usage()
	}
	op, inPath, outPath := os.Args[1], os.Args[2], os.Args[3]

	in, err := os.Open(inPath)
	if err != nil {
		fatal(err)
	}
	defer in.Close()

	out, err := os.Create(outPath)
	if err != nil {
		fatal(err)
	}

	switch op {
	case "encode":
		data, err := os.ReadFile(inPath)
		if err != nil {
			fatal(err)
		}
		if err := arcode.Encode(out, data); err != nil {
			fatal(err)
		}
		report(inPath, outPath, len(data))
	case "decode":
		budget := uint64(defaultBudget)
		if len(os.Args) > 4 {
			v, err := strconv.ParseUint(os.Args[4], 10, 64)
			if err != nil {
				fatal(err)
			}
			budget = v
		}
		data, err := arcode.Decode(in, budget)
		if err != nil {
			fatal(err)
		}
		if _, err := out.Write(data); err != nil {
			fatal(err)
		}
		report(inPath, outPath, len(data))
	default:
		usage()
	}
}

func report(inPath, outPath string, n int) {
	if fi, err := os.Stat(outPath); err == nil {
		fmt.Printf("%s -> %s: 原始 %d 字节, 输出 %d 字节\n", inPath, outPath, n, fi.Size())
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
