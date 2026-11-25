package capsum

import (
	"fmt"
	"sort"

	"prescreen.local/ps-2237843e01/pathlang"
)

// index holds resolved lookup tables built once from a validated Program.
type index struct {
	prog *Program

	nodeSet map[string]struct{}
	domains map[string]*Domain
	roots   map[string]*Root

	out map[string][]int
	in  map[string][]int

	pairReturn  map[int]int
	pairCall    map[int]int
	taskReturn  map[int]int
	taskConsume map[int]int

	componentAlphabet []string
	alphabet          []string

	generators []*pathlang.DFA
	genIndex   map[string]int

	domainGens map[string][]int

	handleIndex map[string]int
	handles     []string
}

func generatorKey(g Grant) string {
	comps := append([]string(nil), g.Components...)
	sort.Strings(comps)
	return fmt.Sprintf("%s|%s|%s", g.Kind, g.Root, joinComps(comps))
}

func joinComps(comps []string) string {
	out := ""
	for i, c := range comps {
		if i > 0 {
			out += "/"
		}
		out += c
	}
	return out
}

func (x *index) buildGenerator(g Grant) (*pathlang.DFA, error) {
	var d *pathlang.DFA
	var err error
	switch g.Kind {
	case GrantSelf:
		d, err = pathlang.BuildSelf(x.alphabet, x.componentAlphabet, g.Root, g.Components)
	case GrantChild:
		d, err = pathlang.BuildChild(x.alphabet, x.componentAlphabet, g.Root, g.Components)
	case GrantDesc:
		d, err = pathlang.BuildDesc(x.alphabet, x.componentAlphabet, g.Root, g.Components)
	default:
		return nil, fmt.Errorf("capsum: unknown grant kind %q", g.Kind)
	}
	if err != nil {
		return nil, err
	}
	return pathlang.Minimize(d), nil
}

func dedupInts(in []int) []int {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

func (x *index) domainUnion(names []string) []int {
	seen := map[int]struct{}{}
	var out []int
	for _, name := range names {
		for _, id := range x.domainGens[name] {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Ints(out)
	return out
}
