package plan

import (
	"fmt"
	"sort"
)

// Source 是一个可下载的制品来源（镜像）。同一版本可来自多个来源，字节数可不同。
type Source struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Available bool   `json:"available"`
}

// Dep 是一条依赖声明：对某个包的版本约束。
type Dep struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
}

// PkgVersion 是注册表中某包的一个版本。注册表允许同一 (name, version)
// 出现多次（不同来源），加载时会按版本合并其来源列表。
type PkgVersion struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Sources []Source `json:"sources"`
	Deps    []Dep    `json:"deps"`

	ver     Version
	depCons []Constraint // 与 Deps 对齐的已解析约束
}

// Registry 是包注册表。
type Registry struct {
	Packages []PkgVersion `json:"packages"`

	index map[string][]*PkgVersion // name -> 按版本降序
}

// LockEntry 是锁文件中一个已固定的解析结果。
type LockEntry struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

// Lock 是锁文件：根需求 + 固定条目。
type Lock struct {
	Roots   []Dep       `json:"roots"`
	Entries []LockEntry `json:"entries"`
}

// Finalize 解析版本与约束、合并同版本多来源条目并建立索引。
func (r *Registry) Finalize() error {
	var uniq []PkgVersion
	pos := make(map[string]int) // name+"\x00"+version -> uniq 下标
	seenID := make(map[int]map[string]bool)
	for i := range r.Packages {
		src := &r.Packages[i]
		v, err := ParseVersion(src.Version)
		if err != nil {
			return fmt.Errorf("包 %s: %v", src.Name, err)
		}
		key := src.Name + "\x00" + v.String()
		if idx, ok := pos[key]; ok {
			// 同版本不同来源：合并来源（按 ID 去重），依赖以先出现者为准。
			for _, s := range src.Sources {
				if !seenID[idx][s.ID] {
					uniq[idx].Sources = append(uniq[idx].Sources, s)
					seenID[idx][s.ID] = true
				}
			}
			continue
		}
		pv := PkgVersion{
			Name:    src.Name,
			Version: v.String(),
			Sources: append([]Source(nil), src.Sources...),
			Deps:    append([]Dep(nil), src.Deps...),
			ver:     v,
		}
		pv.depCons = make([]Constraint, len(pv.Deps))
		for j, d := range pv.Deps {
			c, err := ParseConstraint(d.Constraint)
			if err != nil {
				return fmt.Errorf("包 %s@%s 的依赖 %s: %v", pv.Name, pv.Version, d.Name, err)
			}
			pv.depCons[j] = c
		}
		pos[key] = len(uniq)
		ids := make(map[string]bool)
		for _, s := range pv.Sources {
			ids[s.ID] = true
		}
		seenID[len(uniq)] = ids
		uniq = append(uniq, pv)
	}
	r.Packages = uniq
	r.reindex()
	return nil
}

func (r *Registry) reindex() {
	r.index = make(map[string][]*PkgVersion)
	for i := range r.Packages {
		pv := &r.Packages[i]
		r.index[pv.Name] = append(r.index[pv.Name], pv)
	}
	for name := range r.index {
		vs := r.index[name]
		sort.Slice(vs, func(i, j int) bool { return vs[i].ver.Compare(vs[j].ver) > 0 })
	}
}

// Versions 返回某包全部版本（降序）。
func (r *Registry) Versions(name string) []*PkgVersion {
	return r.index[name]
}

// Lookup 返回指定版本的条目，不存在时返回 nil。
func (r *Registry) Lookup(name, version string) *PkgVersion {
	v, err := ParseVersion(version)
	if err != nil {
		return nil
	}
	for _, pv := range r.index[name] {
		if pv.ver == v {
			return pv
		}
	}
	return nil
}

// availableSources 返回当前可用来源。
func (pv *PkgVersion) availableSources() []Source {
	var out []Source
	for _, s := range pv.Sources {
		if s.Available {
			out = append(out, s)
		}
	}
	return out
}

// lockMap 返回 name -> 条目 的映射。
func (l *Lock) lockMap() map[string]LockEntry {
	m := make(map[string]LockEntry, len(l.Entries))
	for _, e := range l.Entries {
		m[e.Name] = e
	}
	return m
}
