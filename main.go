package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const maxJumps = 40

var errRejected = errors.New("operation rejected")

type node struct {
	isDir    bool
	isLink   bool
	target   string
	children map[string]*node
}

func newDir() *node { return &node{isDir: true, children: map[string]*node{}} }

type vfs struct {
	root *node
}

// resolve walks path segment by segment from the virtual root. Symlinks on
// intermediate segments are always substituted immediately, relative to the
// directory containing the link; the final segment is followed only when
// followFinal is true. Missing intermediate directories are created
// implicitly. It returns the parent directory, the final segment name, and
// the existing node at that name (nil when absent).
func (fs *vfs) resolve(path string, followFinal bool) (*node, string, *node, error) {
	if strings.HasPrefix(path, "/") {
		return nil, "", nil, errRejected // absolute write path
	}
	segs := strings.Split(path, "/")
	stack := []*node{fs.root}
	jumps := 0
	i := 0
	for i < len(segs) {
		seg := segs[i]
		switch seg {
		case "", ".":
			i++
			continue
		case "..":
			if len(stack) == 1 {
				return nil, "", nil, errRejected // escapes virtual root
			}
			stack = stack[:len(stack)-1]
			i++
			continue
		}
		cur := stack[len(stack)-1]
		last := i == len(segs)-1
		child, ok := cur.children[seg]
		if !ok {
			if last {
				return cur, seg, nil, nil
			}
			dir := newDir()
			cur.children[seg] = dir
			stack = append(stack, dir)
			i++
			continue
		}
		if child.isLink && (!last || followFinal) {
			jumps++
			if jumps > maxJumps {
				return nil, "", nil, errRejected // too many jumps
			}
			if strings.HasPrefix(child.target, "/") {
				return nil, "", nil, errRejected // absolute link target followed
			}
			targetSegs := strings.Split(child.target, "/")
			rest := segs[i+1:]
			merged := make([]string, 0, len(targetSegs)+len(rest))
			merged = append(merged, targetSegs...)
			merged = append(merged, rest...)
			segs = merged
			i = 0
			continue
		}
		if last {
			return cur, seg, child, nil
		}
		if !child.isDir {
			return nil, "", nil, errRejected // not a directory
		}
		stack = append(stack, child)
		i++
	}
	return nil, "", nil, errRejected // empty path
}

func (fs *vfs) apply(op []string) error {
	switch op[0] {
	case "L":
		if len(op) != 3 {
			return errRejected
		}
		parent, name, _, err := fs.resolve(op[1], false)
		if err != nil {
			return err
		}
		// L replaces the final-segment entry (e.g. an old link) itself.
		parent.children[name] = &node{isLink: true, target: op[2]}
		return nil
	case "F":
		if len(op) != 2 {
			return errRejected
		}
		parent, name, _, err := fs.resolve(op[1], true)
		if err != nil {
			return err
		}
		parent.children[name] = &node{}
		return nil
	}
	return errRejected
}

func firstFailure(ops [][]string) int {
	fs := &vfs{root: newDir()}
	for idx, op := range ops {
		if len(op) == 0 || fs.apply(op) != nil {
			return idx
		}
	}
	return -1
}

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Println(-1)
		return
	}
	var ops [][]string
	if err := json.Unmarshal(data, &ops); err != nil {
		fmt.Println(-1)
		return
	}
	fmt.Println(firstFailure(ops))
}
