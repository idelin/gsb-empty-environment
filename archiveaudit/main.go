package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/big"
	"os"
)

const usage = `归档审计工具：按块逻辑长度加权、无放回抽样，验证抽中块摘要。

用法:
  archiveaudit create    -out FILE -blocks N [-maxlen M]   生成演示归档（自带块摘要）
  archiveaudit audit     -archive FILE -seed S -n K        抽样审计，输出抽样过程与验证结果
  archiveaudit enumcheck -archive FILE [-limit N]          小归档枚举随机域，核对抽样概率

说明:
  - 抽样由调用方提供的种子完全决定，可复现；恢复/追加抽样不会改变已选块。
  - 总逻辑长度使用大整数，拒绝采样保证无取模偏差。
  - 抽样通过仅代表被抽中的块完整，不代表全文件完整。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "create":
		cmdCreate(os.Args[2:])
	case "audit":
		cmdAudit(os.Args[2:])
	case "enumcheck":
		cmdEnumCheck(os.Args[2:])
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func cmdCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	out := fs.String("out", "", "输出归档文件")
	n := fs.Int("blocks", 8, "块数量")
	maxLen := fs.Int64("maxlen", 4096, "单块最大逻辑长度")
	fs.Parse(args)
	if *out == "" || *n <= 0 {
		fmt.Fprintln(os.Stderr, "create 需要 -out 与 -blocks")
		os.Exit(2)
	}
	a := &Archive{}
	for i := 0; i < *n; i++ {
		size := 1 + int(randInt63n(*maxLen))
		data := make([]byte, size)
		if _, err := rand.Read(data); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		sum := sha256.Sum256(data)
		a.Blocks = append(a.Blocks, &Block{
			ID:     fmt.Sprintf("block-%03d", i),
			Length: big.NewInt(int64(size)),
			Digest: hex.EncodeToString(sum[:]),
			Data:   data,
		})
	}
	if err := a.Save(*out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("已生成归档 %s（%d 块，总逻辑长度 %s）\n", *out, len(a.Blocks), a.TotalLength())
}

func cmdAudit(args []string) {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	path := fs.String("archive", "", "归档文件")
	seed := fs.String("seed", "", "调用方提供的抽样种子（相同种子可复现）")
	k := fs.Int("n", 0, "抽样块数")
	fs.Parse(args)
	if *path == "" || *seed == "" || *k <= 0 {
		fmt.Fprintln(os.Stderr, "audit 需要 -archive、-seed 与 -n")
		os.Exit(2)
	}
	a, err := LoadArchive(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !RunAudit(a, *seed, *k, os.Stdout) {
		os.Exit(1)
	}
}

func cmdEnumCheck(args []string) {
	fs := flag.NewFlagSet("enumcheck", flag.ExitOnError)
	path := fs.String("archive", "", "归档文件")
	limit := fs.Int64("limit", 1<<20, "随机域枚举上限")
	fs.Parse(args)
	if *path == "" {
		fmt.Fprintln(os.Stderr, "enumcheck 需要 -archive")
		os.Exit(2)
	}
	a, err := LoadArchive(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res, err := EnumCheck(a, *limit)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res.PrintReport(a, os.Stdout)
	if len(res.Verify(a)) > 0 {
		os.Exit(1)
	}
}

func randInt63n(n int64) int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	v := int64(b[0]) | int64(b[1])<<8 | int64(b[2])<<16 | int64(b[3])<<24 |
		int64(b[4])<<32 | int64(b[5])<<40 | int64(b[6])<<48 | int64(b[7]&0x7f)<<56
	return v % n // 仅用于生成演示数据，与抽样无关
}
