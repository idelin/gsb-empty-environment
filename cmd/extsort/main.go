// Command extsort sorts text segments ("key\tvalue" lines) externally:
// it writes sorted runs, then merges them with checkpoints so an
// interrupted merge resumes from the last published checkpoint.
package main

import (
	"flag"
	"fmt"
	"os"

	"extsort"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sort":
		sortCmd(os.Args[2:])
	case "merge":
		mergeCmd(os.Args[2:])
	case "cat":
		catCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  extsort sort  -dir WORK -o OUT [-mem N] [-fanin N] [-ckpt N] INPUT...
  extsort merge -dir WORK -o OUT [-fanin N] [-ckpt N]   (resume the merge)
  extsort cat   FILE                                    (decode as text)
`)
}

func addCommon(fs *flag.FlagSet, o *extsort.Options, out *string) {
	fs.StringVar(&o.Dir, "dir", "extsort-work", "working directory for runs and checkpoints")
	fs.StringVar(out, "o", "output.bin", "final output file (inside -dir)")
	fs.IntVar(&o.FanIn, "fanin", 64, "max input segments per merge task")
	fs.IntVar(&o.CheckpointEvery, "ckpt", 10000, "records between checkpoints")
}

func sortCmd(args []string) {
	fs := flag.NewFlagSet("sort", flag.ExitOnError)
	var o extsort.Options
	var out string
	addCommon(fs, &o, &out)
	fs.IntVar(&o.MemRecords, "mem", 100000, "records held in memory per run")
	fs.Parse(args)
	inputs := fs.Args()
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "sort: no input segments")
		os.Exit(2)
	}
	runs, err := extsort.GenerateRuns(inputs, o)
	must(err)
	fmt.Printf("wrote %d sorted runs to %s\n", len(runs), o.Dir)
	b, err := extsort.Merge(out, o)
	must(err)
	fmt.Printf("merged %d records into %s (peak open files: %d)\n", b.Records, out, extsort.PeakOpenFiles())
}

func mergeCmd(args []string) {
	fs := flag.NewFlagSet("merge", flag.ExitOnError)
	var o extsort.Options
	var out string
	addCommon(fs, &o, &out)
	fs.Parse(args)
	b, err := extsort.Merge(out, o)
	must(err)
	fmt.Printf("merged %d records into %s (peak open files: %d)\n", b.Records, out, extsort.PeakOpenFiles())
}

func catCmd(args []string) {
	fs := flag.NewFlagSet("cat", flag.ExitOnError)
	fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "cat: need exactly one file")
		os.Exit(2)
	}
	recs, err := extsort.DecodeFile(fs.Arg(0))
	must(err)
	for i := range recs {
		fmt.Printf("%s\t%s\n", recs[i].Key, recs[i].Value)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
