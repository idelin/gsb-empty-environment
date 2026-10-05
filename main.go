package main

import (
	"flag"
	"fmt"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `logrec - 追加日志恢复工具

用法:
  logrec recover -data D -index I     恢复数据/索引文件对，输出修复范围
  logrec append  -data D -index I [-sync] MSG...  追加记录
  logrec list    -data D -index I     列出全部记录
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	data := fs.String("data", "log.data", "数据文件路径")
	index := fs.String("index", "log.index", "索引文件路径")
	sync := fs.Bool("sync", false, "append 后 fsync")
	fs.Parse(os.Args[2:])

	switch os.Args[1] {
	case "recover":
		rep, err := Recover(*data, *index)
		fmt.Println("repair report:")
		fmt.Println(rep)
		if err != nil {
			fmt.Fprintln(os.Stderr, "recover failed:", err)
			os.Exit(1)
		}
	case "append":
		s, _, err := Open(*data, *index, *sync)
		if err == ErrCorruptRecord {
			fmt.Fprintln(os.Stderr, "warning: corrupt record found, using valid prefix only")
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "open failed:", err)
			os.Exit(1)
		}
		defer s.Close()
		for _, m := range fs.Args() {
			i, err := s.Append([]byte(m))
			if err != nil {
				fmt.Fprintln(os.Stderr, "append failed:", err)
				os.Exit(1)
			}
			fmt.Printf("appended #%d\n", i)
		}
	case "list":
		s, _, err := Open(*data, *index, false)
		if err == ErrCorruptRecord {
			fmt.Fprintln(os.Stderr, "warning: corrupt record found, using valid prefix only")
		} else if err != nil {
			fmt.Fprintln(os.Stderr, "open failed:", err)
			os.Exit(1)
		}
		defer s.Close()
		for i := 0; i < s.Len(); i++ {
			p, err := s.Get(i)
			if err != nil {
				fmt.Fprintln(os.Stderr, "read failed:", err)
				os.Exit(1)
			}
			fmt.Printf("#%d %s\n", i, p)
		}
	default:
		usage()
	}
}
