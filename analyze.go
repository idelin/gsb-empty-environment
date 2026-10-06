package testsel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FuncIndex maps source positions to function IDs so coverage profiles can
// be attributed to functions.
type FuncIndex struct {
	files map[string][]funcSpan // key: "<pkgpath>/<file base name>"
}

type funcSpan struct {
	id               FuncID
	startLine, endLn int
}

// Analyze parses every package under root (skipping vendor, testdata and
// hidden directories, and ignoring _test.go files) and builds a snapshot:
// a content hash per function, the static call graph, and a conservative
// model of dynamic dispatch. modulePath is the module's import path; when
// empty it is read from root/go.mod.
//
// The analysis is deliberately heuristic and type-free: calls through a
// package-qualified identifier are resolved statically, everything else
// (interface values, function values, method values) is recorded as a
// dynamic call to the method name and over-approximated via DynImpls.
func Analyze(root, modulePath string) (*Snapshot, *FuncIndex, error) {
	if modulePath == "" {
		mp, err := readModulePath(filepath.Join(root, "go.mod"))
		if err != nil {
			return nil, nil, err
		}
		modulePath = mp
	}
	dirs, err := goDirs(root)
	if err != nil {
		return nil, nil, err
	}

	a := &analyzer{
		root:    root,
		module:  modulePath,
		snap:    NewSnapshot(),
		idx:     &FuncIndex{files: map[string][]funcSpan{}},
		pkgFunc: map[string]map[string]FuncID{},
		files:   map[*ast.File]*fileInfo{},
		fset:    token.NewFileSet(),
	}
	for _, d := range dirs {
		if err := a.parseDir(d); err != nil {
			return nil, nil, err
		}
	}
	a.indexDecls()
	a.buildEdges()
	a.snap.Normalize()
	return a.snap, a.idx, nil
}

type fileInfo struct {
	pkgPath string
	imports map[string]string // alias -> import path
}

type analyzer struct {
	root    string
	module  string
	snap    *Snapshot
	idx     *FuncIndex
	pkgFunc map[string]map[string]FuncID // pkgPath -> func name -> id
	files   map[*ast.File]*fileInfo
	fset    *token.FileSet
}

func goDirs(root string) ([]string, error) {
	seen := map[string]bool{}
	var dirs []string
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			name := fi.Name()
			if path != root && (name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go") {
			d := filepath.Dir(path)
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs, err
}

func (a *analyzer) parseDir(dir string) error {
	rel, err := filepath.Rel(a.root, dir)
	if err != nil {
		return err
	}
	pkgPath := a.module
	if rel != "." {
		pkgPath = a.module + "/" + filepath.ToSlash(rel)
	}
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		f, err := parser.ParseFile(a.fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("testsel: %s: %v", path, err)
		}
		imports := map[string]string{}
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			alias := filepath.Base(p)
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			imports[alias] = p
		}
		a.files[f] = &fileInfo{pkgPath: pkgPath, imports: imports}
		a.idx.files[pkgPath+"/"+name] = nil
	}
	return nil
}

// indexDecls assigns IDs and hashes to every declared function.
func (a *analyzer) indexDecls() {
	for f, info := range a.files {
		pos := a.fset.Position(f.Pos())
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			id := funcID(info.pkgPath, fn)
			a.snap.Funcs[id] = a.hash(fn)
			start := a.fset.Position(fn.Pos())
			end := a.fset.Position(fn.End())
			key := info.pkgPath + "/" + filepath.Base(pos.Filename)
			a.idx.files[key] = append(a.idx.files[key], funcSpan{id: id, startLine: start.Line, endLn: end.Line})
			if fn.Recv == nil {
				if a.pkgFunc[info.pkgPath] == nil {
					a.pkgFunc[info.pkgPath] = map[string]FuncID{}
				}
				a.pkgFunc[info.pkgPath][fn.Name.Name] = id
			} else {
				a.snap.DynImpls[fn.Name.Name] = append(a.snap.DynImpls[fn.Name.Name], id)
			}
		}
	}
	for k := range a.idx.files {
		spans := a.idx.files[k]
		sort.Slice(spans, func(i, j int) bool { return spans[i].startLine < spans[j].startLine })
		a.idx.files[k] = spans
	}
}

// buildEdges walks function bodies resolving static calls and recording
// dynamic ones.
func (a *analyzer) buildEdges() {
	edgeSeen := map[Edge]bool{}
	dynSeen := map[FuncID]map[string]bool{}
	for f, info := range a.files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := funcID(info.pkgPath, fn)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if id, ok := a.pkgFunc[info.pkgPath][fun.Name]; ok && id != caller {
						e := Edge{Caller: caller, Callee: id}
						if !edgeSeen[e] {
							edgeSeen[e] = true
							a.snap.Edges = append(a.snap.Edges, e)
						}
					}
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok {
						if imp, isPkg := info.imports[x.Name]; isPkg {
							if id, ok := a.pkgFunc[imp][fun.Sel.Name]; ok {
								e := Edge{Caller: caller, Callee: id}
								if !edgeSeen[e] {
									edgeSeen[e] = true
									a.snap.Edges = append(a.snap.Edges, e)
								}
								return true
							}
						}
					}
					// Not a package-qualified call: dynamic dispatch.
					if dynSeen[caller] == nil {
						dynSeen[caller] = map[string]bool{}
					}
					if !dynSeen[caller][fun.Sel.Name] {
						dynSeen[caller][fun.Sel.Name] = true
						a.snap.DynCalls[caller] = append(a.snap.DynCalls[caller], fun.Sel.Name)
					}
				}
				return true
			})
		}
	}
}

func (a *analyzer) hash(fn *ast.FuncDecl) string {
	var buf bytes.Buffer
	// Positions are irrelevant to the printer's semantic output, so the
	// hash is stable as long as the function's code is unchanged.
	printer.Fprint(&buf, a.fset, fn)
	sum := sha256.Sum256(buf.Bytes())
	return hex.EncodeToString(sum[:])
}

func funcID(pkgPath string, fn *ast.FuncDecl) FuncID {
	if fn.Recv == nil {
		return FuncID(pkgPath + "." + fn.Name.Name)
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		recv = star.X
	}
	name := ""
	if id, ok := recv.(*ast.Ident); ok {
		name = id.Name
	}
	return FuncID(pkgPath + ".(" + name + ")." + fn.Name.Name)
}

func readModulePath(goMod string) (string, error) {
	data, err := ioutil.ReadFile(goMod)
	if err != nil {
		return "", fmt.Errorf("testsel: module path not given and %s unreadable: %v", goMod, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module ")), nil
		}
	}
	return "", fmt.Errorf("testsel: no module directive in %s", goMod)
}
