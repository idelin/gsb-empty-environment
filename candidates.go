package netsec

import "sort"

// Record 是一条记录（匿名记录或外部观察）上各属性域的集合，
// 键为属性名。外部观察中未出现的属性视为无约束。
type Record map[string]Domain

// Observation 是一条外部观察：背景知识 Known 及其声明的主体 Subject。
// Subject 相同的多个 Observation 视为指向同一个体，需联合约束。
type Observation struct {
	Subject string
	Known   Record
}

// Compatible 报告外部观察 ext 是否与匿名记录 anon 兼容：
// ext 中每个已知属性的域都必须与 anon 对应属性的域有交集；
// anon 中缺失该属性（或该属性域为 Missing）时视为兼容。
func Compatible(ext, anon Record) bool {
	for attr, known := range ext {
		anonDom, ok := anon[attr]
		if !ok {
			anonDom = Missing()
		}
		if !known.Intersects(anonDom) {
			return false
		}
	}
	return true
}

// CandidateSet 返回 anon 中与单条外部观察 ext 兼容的全部记录下标（升序）。
func CandidateSet(anon []Record, ext Record) []int {
	var out []int
	for i, rec := range anon {
		if Compatible(ext, rec) {
			out = append(out, i)
		}
	}
	return out
}

// JointCandidates 计算声明同一主体的多条观察的联合候选集，
// 即各观察候选集的交集（而非并集）：该主体必须同时满足所有观察。
// 不传观察时返回空集。
func JointCandidates(anon []Record, obs ...Record) []int {
	if len(obs) == 0 {
		return nil
	}
	cur := CandidateSet(anon, obs[0])
	for _, ext := range obs[1:] {
		next := CandidateSet(anon, ext)
		cur = intersectSorted(cur, next)
		if len(cur) == 0 {
			return nil
		}
	}
	return cur
}

// SubjectCandidates 按声明主体分组，返回每个主体的联合候选集。
// 同一 Subject 的多条观察取交集；不同主体各自独立计算。
// 结果按下标升序；无兼容对象的主体映射到空切片。
func SubjectCandidates(anon []Record, obs []Observation) map[string][]int {
	bySubject := make(map[string][]Record)
	var order []string
	for _, o := range obs {
		if _, seen := bySubject[o.Subject]; !seen {
			order = append(order, o.Subject)
		}
		bySubject[o.Subject] = append(bySubject[o.Subject], o.Known)
	}
	out := make(map[string][]int, len(bySubject))
	for _, subj := range order {
		cands := JointCandidates(anon, bySubject[subj]...)
		if cands == nil {
			cands = []int{}
		}
		out[subj] = cands
	}
	return out
}

// UniquelyIdentified 返回联合候选集恰含一个记录的主体及其命中的记录下标。
func UniquelyIdentified(anon []Record, obs []Observation) map[string]int {
	out := make(map[string]int)
	for subj, cands := range SubjectCandidates(anon, obs) {
		if len(cands) == 1 {
			out[subj] = cands[0]
		}
	}
	return out
}

func intersectSorted(a, b []int) []int {
	var out []int
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] < b[j]:
			i++
		case a[i] > b[j]:
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	return out
}

// SortedCats 返回域中类别集合的排序副本，便于宿主侧展示与比对。
func SortedCats(d Domain) []string {
	out := make([]string, 0, len(d.Cats))
	for v := range d.Cats {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
