// Command cellengine is an interactive/front-end CLI for the cell engine.
// It reads commands from a file (first argument) or stdin.
//
// Commands:
//
//	set <ID> = <expr>   set a cell formula (single-op batch outside begin/end)
//	del <ID>            delete a cell
//	begin               start a batch (all changes commit together)
//	end                 commit the batch as one new version
//	get <ID>            print the current value of a cell
//	get <ID>@<version>  print the value at a past version (read-only)
//	version             print the current version
//	dump                list all cells with formulas and current values
//	help                show this help
//	quit                exit
//
// Lines starting with '#' and blank lines are ignored.
package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"cellengine/engine"
)

const helpText = `commands:
  set <ID> = <expr>   set a cell formula
  del <ID>            delete a cell
  begin / end         group sets and deletes into one atomic batch
  get <ID>[@version]  query a cell (current or past version)
  version             show current version
  dump                list all cells
  help                show this help
  quit                exit
formula syntax:
  expr    := add
  add     := mul (('+'|'-') mul)*
  mul     := unary ('*' unary)*
  unary   := '-' unary | primary
  primary := INT | IDENT | #ERR! | #REF! | #CYCLE!
           | IF(expr, expr, expr) | '(' expr ')'
  IDENT   := [A-Za-z_][A-Za-z0-9_]*  (cell id, case sensitive, except IF)
IF evaluates only the selected branch; only references actually read
become dependencies.`

func main() {
	var in io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}
	run(in, os.Stdout)
}

func run(in io.Reader, out io.Writer) {
	eng := engine.New()
	inBatch := false
	sets := map[string]string{}
	var dels []string

	commit := func() {
		rec, err := eng.Apply(sets, dels)
		switch {
		case err != nil:
			fmt.Fprintln(out, "error:", err)
		case rec == nil:
			fmt.Fprintln(out, "ok (no changes)")
		default:
			fmt.Fprintf(out, "ok v%d recomputed=[%s]\n", eng.Version(), strings.Join(rec, " "))
		}
		sets = map[string]string{}
		dels = nil
	}

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		switch strings.ToLower(fields[0]) {
		case "set":
			eq := strings.Index(line, "=")
			if eq < 0 {
				fmt.Fprintln(out, "error: expected: set <ID> = <expr>")
				continue
			}
			id := strings.TrimSpace(line[len("set"):eq])
			src := strings.TrimSpace(line[eq+1:])
			sets[id] = src
			dels = removeStr(dels, id)
			if !inBatch {
				commit()
			}
		case "del":
			if len(fields) != 2 {
				fmt.Fprintln(out, "error: expected: del <ID>")
				continue
			}
			delete(sets, fields[1])
			dels = append(dels, fields[1])
			if !inBatch {
				commit()
			}
		case "begin":
			if inBatch {
				fmt.Fprintln(out, "error: already in a batch")
				continue
			}
			inBatch = true
		case "end":
			if !inBatch {
				fmt.Fprintln(out, "error: not in a batch")
				continue
			}
			inBatch = false
			commit()
		case "get":
			if len(fields) != 2 {
				fmt.Fprintln(out, "error: expected: get <ID>[@version]")
				continue
			}
			id, ver := fields[1], eng.Version()
			if at := strings.Index(id, "@"); at >= 0 {
				n, err := strconv.Atoi(id[at+1:])
				if err != nil {
					fmt.Fprintln(out, "error: bad version:", id[at+1:])
					continue
				}
				id, ver = id[:at], n
			}
			v, ok := eng.Get(id, ver)
			if !ok {
				fmt.Fprintf(out, "%s@v%d: <no such cell>\n", id, ver)
			} else {
				fmt.Fprintf(out, "%s@v%d = %s\n", id, ver, v)
			}
		case "version":
			fmt.Fprintln(out, eng.Version())
		case "dump":
			for _, id := range eng.Cells() {
				src, _ := eng.Formula(id)
				v, _ := eng.Get(id, eng.Version())
				fmt.Fprintf(out, "%s = %s  =>  %s\n", id, src, v)
			}
		case "help":
			fmt.Fprintln(out, helpText)
		case "quit", "exit":
			return
		default:
			fmt.Fprintln(out, "error: unknown command:", fields[0])
		}
	}
}

func removeStr(s []string, x string) []string {
	out := s[:0]
	for _, v := range s {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
