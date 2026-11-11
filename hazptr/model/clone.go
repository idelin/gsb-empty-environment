package model

import (
	"fmt"
	"sort"
	"strings"
)

func cloneMachine(m *Machine) *Machine {
	c := &Machine{
		cfg:       m.cfg,
		threads:   map[int]*thread{},
		objects:   map[string][]*object{},
		retired:   map[string]retiredEntry{},
		step:      m.step,
		scanGen:   m.scanGen,
		nextIdent: m.nextIdent,
		trace:     append(Trace{}, m.trace...),
	}
	for id, t := range m.threads {
		ct := &thread{
			id:           id,
			stagedRoots:  map[string]string{},
			stagedHP:     map[int]hpView{},
			visibleRoots: map[string]string{},
			visibleHP:    map[int]hpView{},
			regs:         map[string]*regState{},
			aborted:      t.aborted,
		}
		for k, v := range t.stagedRoots {
			ct.stagedRoots[k] = v
		}
		for k, v := range t.stagedHP {
			ct.stagedHP[k] = v
		}
		for k, v := range t.visibleRoots {
			ct.visibleRoots[k] = v
		}
		for k, v := range t.visibleHP {
			ct.visibleHP[k] = v
		}
		for k, v := range t.regs {
			rv := *v
			ct.regs[k] = &rv
		}
		c.threads[id] = ct
	}
	for addr, gens := range m.objects {
		cp := make([]*object, len(gens))
		for i, o := range gens {
			ov := *o
			cp[i] = &ov
		}
		c.objects[addr] = cp
	}
	for addr, entry := range m.retired {
		c.retired[addr] = entry
	}
	return c
}

// fingerprint identifies a machine state plus continuation point. Map
// contents are emitted in sorted key order so equal states hash identically.
func fingerprint(m *Machine, pc map[int]int) string {
	var b strings.Builder
	threadIDs := m.allThreadIDs()

	fmt.Fprintf(&b, "cfg:%v|", m.cfg)
	for _, id := range threadIDs {
		t := m.threads[id]
		fmt.Fprintf(&b, "T%d{pc:%d abort:%v", id, pc[id], t.aborted)

		writeStringMap(&b, "sroot", t.stagedRoots)
		writeStringMap(&b, "vroot", t.visibleRoots)
		writeHPMap(&b, "shp", t.stagedHP)
		writeHPMap(&b, "vhp", t.visibleHP)

		regKeys := make([]string, 0, len(t.regs))
		for k := range t.regs {
			regKeys = append(regKeys, k)
		}
		sort.Strings(regKeys)
		b.WriteString(" regs:")
		for _, k := range regKeys {
			r := t.regs[k]
			fmt.Fprintf(&b, "%s=(%s,g%d,v%d@%d);", k, r.addr, r.identity, b2i(r.verified), r.verifiedAt)
		}
		b.WriteString("}")
	}

	objAddrs := make([]string, 0, len(m.objects))
	for addr := range m.objects {
		objAddrs = append(objAddrs, addr)
	}
	sort.Strings(objAddrs)
	b.WriteString("objs:")
	for _, addr := range objAddrs {
		for _, o := range m.objects[addr] {
			fmt.Fprintf(&b, "%s@g%d(f%d@%d);", o.addr, o.identity, b2i(o.freed), o.freedByScan)
		}
	}

	retiredAddrs := make([]string, 0, len(m.retired))
	for addr := range m.retired {
		retiredAddrs = append(retiredAddrs, addr)
	}
	sort.Strings(retiredAddrs)
	b.WriteString(" retired:")
	for _, addr := range retiredAddrs {
		e := m.retired[addr]
		fmt.Fprintf(&b, "%s@g%d@%d;", e.addr, e.identity, e.retiredAt)
	}
	fmt.Fprintf(&b, " scan:%d ident:%d", m.scanGen, m.nextIdent)
	return b.String()
}

func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}

func writeStringMap(b *strings.Builder, tag string, in map[string]string) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(b, " %s:", tag)
	for _, k := range keys {
		fmt.Fprintf(b, "%s=%s;", k, in[k])
	}
}

func writeHPMap(b *strings.Builder, tag string, in map[int]hpView) {
	keys := make([]int, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	fmt.Fprintf(b, " %s:", tag)
	for _, k := range keys {
		v := in[k]
		fmt.Fprintf(b, "%d=(%s,g%d,ok%d);", k, v.addr, v.identity, b2i(v.valid))
	}
}
