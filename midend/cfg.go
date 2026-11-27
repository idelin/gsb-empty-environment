package midend

// cfgInfo holds control-flow graph and dominator information for a function.
type cfgInfo struct {
	fn     *Func
	order  []*Block // reverse postorder (entry first)
	preds  map[string][]*Block
	succs  map[string][]*Block
	idom   map[string]string // immediate dominator block name
	domF   map[string]map[string]bool
	dfront map[string]map[string]bool
}

func buildCFG(f *Func) *cfgInfo {
	c := &cfgInfo{
		fn:     f,
		preds:  map[string][]*Block{},
		succs:  map[string][]*Block{},
		idom:   map[string]string{},
		domF:   map[string]map[string]bool{},
		dfront: map[string]map[string]bool{},
	}
	for _, b := range f.Blocks {
		for _, s := range successors(b) {
			c.succs[b.Name] = append(c.succs[b.Name], s)
			c.preds[s.Name] = append(c.preds[s.Name], b)
		}
	}
	c.order = reversePostorder(f, c.succs)
	c.computeDominators()
	c.computeDomFrontiers()
	return c
}

func successors(b *Block) []*Block {
	switch t := b.Term.(type) {
	case *Jump:
		return []*Block{{Name: t.To}}
	case *Branch:
		return []*Block{{Name: t.T}, {Name: t.F}}
	default:
		return nil
	}
}

func reversePostorder(f *Func, succs map[string][]*Block) []*Block {
	visited := map[string]bool{}
	var post []*Block
	var dfs func(b *Block)
	dfs = func(b *Block) {
		if visited[b.Name] {
			return
		}
		visited[b.Name] = true
		for _, s := range succs[b.Name] {
			dfs(&Block{Name: s.Name})
		}
		post = append(post, b)
	}
	dfs(f.Blocks[0])
	out := make([]*Block, 0, len(post))
	for i := len(post) - 1; i >= 0; i-- {
		out = append(out, f.blockByName(post[i].Name))
	}
	return out
}

func (f *Func) blockByName(name string) *Block {
	for _, b := range f.Blocks {
		if b.Name == name {
			return b
		}
	}
	return nil
}

// computeDominators uses the iterative dataflow formulation of Cooper,
// Harvey and Kennedy over reverse postorder.
func (c *cfgInfo) computeDominators() {
	entry := c.order[0].Name
	c.idom[entry] = entry
	changed := true
	for changed {
		changed = false
		for _, b := range c.order[1:] {
			var newIDom string
			for _, p := range c.preds[b.Name] {
				if _, ok := c.idom[p.Name]; !ok {
					continue
				}
				if newIDom == "" {
					newIDom = p.Name
				} else {
					newIDom = c.intersect(p.Name, newIDom)
				}
			}
			if newIDom != "" && c.idom[b.Name] != newIDom {
				c.idom[b.Name] = newIDom
				changed = true
			}
		}
	}

	// Materialize full dominator sets for convenience.
	for _, b := range c.order {
		set := map[string]bool{}
		cur := b.Name
		for {
			set[cur] = true
			if cur == entry {
				break
			}
			parent, ok := c.idom[cur]
			if !ok {
				break
			}
			cur = parent
		}
		c.domF[b.Name] = set
	}
}

func (c *cfgInfo) intersect(b1, b2 string) string {
	f1 := c.poIndex(b1)
	f2 := c.poIndex(b2)
	for b1 != b2 {
		for f1 > f2 {
			b1 = c.idom[b1]
			f1 = c.poIndex(b1)
		}
		for f2 > f1 {
			b2 = c.idom[b2]
			f2 = c.poIndex(b2)
		}
	}
	return b1
}

func (c *cfgInfo) poIndex(name string) int {
	for i, b := range c.order {
		if b.Name == name {
			return i
		}
	}
	return -1
}

// computeDomFrontiers applies the standard dominance-frontier algorithm.
func (c *cfgInfo) computeDomFrontiers() {
	for _, b := range c.order {
		if len(c.preds[b.Name]) < 2 {
			continue
		}
		for _, p := range c.preds[b.Name] {
			runner := p.Name
			for runner != c.idom[b.Name] {
				set := c.dfront[runner]
				if set == nil {
					set = map[string]bool{}
					c.dfront[runner] = set
				}
				set[b.Name] = true
				runner = c.idom[runner]
			}
		}
	}
}

// dominates reports whether block a dominates block b.
func (c *cfgInfo) dominates(a, b string) bool {
	return c.domF[b][a]
}
