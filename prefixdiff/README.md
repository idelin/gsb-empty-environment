# prefixdiff

Diff two prefix-rule permission sets (`old` vs `new`) over a finite alphabet.
Paths are the strings of length exactly `maxdepth`; a rule prefix matches every
path it is a prefix of. Conflict resolution: **deeper prefix wins; at equal
depth deny wins**; unmatched paths fall back to `default` (deny unless set).

The tool prints the **minimal disjoint prefix sets** for:

- **expanded** — paths denied by `old` but allowed by `new`
- **shrunk** — paths allowed by `old` but denied by `new`

Because the emitted prefixes are the maximal subtrees that lie entirely inside
each region, the cover is unique and therefore minimal. Every result is then
**verified by enumerating all paths**: coverage must match the rule semantics
exactly, prefixes must be pairwise disjoint (within and across both sets), and
no prefix may be redundant.

> Why fixed-length paths: with variable-length paths a region like "`a`
> expanded but `aa` unchanged" cannot be covered by any prefix set (every
> prefix covering `a` also covers `aa`). The leaf model guarantees an exact
> minimal disjoint cover always exists.

## Usage

```sh
go run . examples/demo.rules      # or: go build -o prefixdiff && ./prefixdiff rules.txt
cat rules.txt | go run .          # reads stdin when no file is given
```

Input format (blank lines and `#` comments allowed):

```
alphabet abc            # each rune is one symbol
maxdepth 3              # paths have exactly this length (>= 1)
default allow|deny      # optional, default deny
old allow a             # <old|new> <allow|deny> <prefix>
new deny -              # "-" is the empty (root) prefix: matches everything
```

Flags: `-maxenum N` refuses to enumerate more than `N` paths (default 20M).
Exit code is non-zero if verification fails.

## Development

```sh
go test ./...
```

Tests include hand-computed cases plus randomized cross-checking of the diff
against full path enumeration.
