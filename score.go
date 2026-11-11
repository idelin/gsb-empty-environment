package rpalocate

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Weights 是各类锚点在总分中的权重，总和为 1。
type Weights struct {
	Text      float64
	Role      float64
	Tag       float64
	Attrs     float64
	Neighbors float64
}

// DefaultWeights 返回默认权重：文本与上下文邻居占主导，
// 标签权重最低，因为改版常把 <button> 换成 <a role="button">。
func DefaultWeights() Weights {
	return Weights{Text: 0.35, Role: 0.10, Tag: 0.05, Attrs: 0.25, Neighbors: 0.25}
}

// Options 控制 Locate 的判定行为。
type Options struct {
	Threshold float64
	Weights   Weights
}

// Option 修改 Options。
type Option func(*Options)

// WithThreshold 设置最低置信度阈值（默认 0.6）。
func WithThreshold(t float64) Option {
	return func(o *Options) { o.Threshold = t }
}

// WithWeights 设置锚点权重。
func WithWeights(w Weights) Option {
	return func(o *Options) { o.Weights = w }
}

// Scored 是一个候选元素的打分结果，Reasons 记录每个锚点的命中情况。
type Scored struct {
	Element *Element
	Score   float64
	Reasons []string
}

// ErrNoCandidates 表示快照中没有任何可交互元素。
var ErrNoCandidates = errors.New("rpalocate: 快照中没有可交互的候选元素")

// AmbiguityError 表示最高分不唯一，运行时必须拒绝点击。
type AmbiguityError struct {
	Score      float64
	Candidates []Scored
}

func (e *AmbiguityError) Error() string {
	return fmt.Sprintf("rpalocate: 定位歧义：%d 个候选同获最高分 %.3f，拒绝点击",
		len(e.Candidates), e.Score)
}

// LowConfidenceError 表示最高分低于阈值，运行时必须拒绝点击。
type LowConfidenceError struct {
	Score     float64
	Threshold float64
	Best      Scored
}

func (e *LowConfidenceError) Error() string {
	return fmt.Sprintf("rpalocate: 最高得分 %.3f 低于阈值 %.3f，拒绝点击",
		e.Score, e.Threshold)
}

// Locate 在 DOM 快照上为所有可交互候选元素打分，返回唯一最高分者。
// 最高分低于阈值时返回 *LowConfidenceError；最高分不唯一时返回
// *AmbiguityError；两种情况下都不应点击任何元素。
func Locate(root *Element, loc Locator, opts ...Option) (Scored, error) {
	o := Options{Threshold: 0.6, Weights: DefaultWeights()}
	for _, opt := range opts {
		opt(&o)
	}
	var cands []*Element
	for _, el := range root.Elements() {
		if actionable(el) {
			cands = append(cands, el)
		}
	}
	if len(cands) == 0 {
		return Scored{}, ErrNoCandidates
	}
	scored := make([]Scored, 0, len(cands))
	for _, c := range cands {
		scored = append(scored, scoreElement(c, loc, o.Weights))
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	top := scored[0]
	if top.Score < o.Threshold {
		return Scored{}, &LowConfidenceError{Score: top.Score, Threshold: o.Threshold, Best: top}
	}
	tied := []Scored{top}
	for _, s := range scored[1:] {
		if math.Abs(s.Score-top.Score) > 1e-9 {
			break
		}
		tied = append(tied, s)
	}
	if len(tied) > 1 {
		return Scored{}, &AmbiguityError{Score: top.Score, Candidates: tied}
	}
	return top, nil
}

func scoreElement(cand *Element, loc Locator, w Weights) Scored {
	var total, got float64
	var reasons []string
	add := func(weight, earned float64, reason string) {
		total += weight
		got += earned
		reasons = append(reasons, reason)
	}
	if loc.Text != "" {
		ct := cand.TextContent()
		switch {
		case ct == loc.Text:
			add(w.Text, w.Text, fmt.Sprintf("文本完全匹配 %q (+%.2f)", loc.Text, w.Text))
		case strings.Contains(ct, loc.Text) || (ct != "" && strings.Contains(loc.Text, ct)):
			part := w.Text * 0.5
			add(w.Text, part, fmt.Sprintf("文本部分匹配：候选 %q，录制 %q (+%.2f)", ct, loc.Text, part))
		default:
			add(w.Text, 0, fmt.Sprintf("文本不匹配：候选 %q，录制 %q", ct, loc.Text))
		}
	}
	if loc.Role != "" {
		if cr := roleOf(cand); cr == loc.Role {
			add(w.Role, w.Role, fmt.Sprintf("角色 %q 匹配 (+%.2f)", loc.Role, w.Role))
		} else {
			add(w.Role, 0, fmt.Sprintf("角色不匹配：候选 %q，录制 %q", cr, loc.Role))
		}
	}
	if loc.Tag != "" {
		if cand.Tag == loc.Tag {
			add(w.Tag, w.Tag, fmt.Sprintf("标签 <%s> 匹配 (+%.2f)", loc.Tag, w.Tag))
		} else {
			add(w.Tag, 0, fmt.Sprintf("标签不匹配：候选 <%s>，录制 <%s>", cand.Tag, loc.Tag))
		}
	}
	if len(loc.Attrs) > 0 {
		share := w.Attrs / float64(len(loc.Attrs))
		for _, a := range loc.Attrs {
			cv, ok := cand.Attr(a.Name)
			switch {
			case !ok:
				add(share, 0, fmt.Sprintf("属性 %s 缺失：录制值 %q", a.Name, a.Value))
			case a.Name == "class":
				r := classOverlap(a.Value, cv)
				add(share, share*r, fmt.Sprintf("属性 class 重合 %.0f%%：候选 %q，录制 %q (+%.2f)",
					r*100, normalize(cv), a.Value, share*r))
			case normalize(cv) == a.Value:
				add(share, share, fmt.Sprintf("属性 %s=%q 匹配 (+%.2f)", a.Name, a.Value, share))
			default:
				add(share, 0, fmt.Sprintf("属性 %s 不匹配：候选 %q，录制 %q", a.Name, normalize(cv), a.Value))
			}
		}
	}
	if len(loc.Neighbors) > 0 {
		share := w.Neighbors / float64(len(loc.Neighbors))
		for _, nb := range loc.Neighbors {
			actual := neighborText(cand, nb.Relation)
			if actual != "" && actual == nb.Text {
				add(share, share, fmt.Sprintf("邻居[%s] %q 匹配 (+%.2f)", nb.Relation, nb.Text, share))
			} else {
				add(share, 0, fmt.Sprintf("邻居[%s] 未命中：期望 %q，实际 %q", nb.Relation, nb.Text, actual))
			}
		}
	}
	score := 0.0
	if total > 0 {
		score = got / total
	}
	return Scored{Element: cand, Score: score, Reasons: reasons}
}

func neighborText(el *Element, relation string) string {
	switch relation {
	case RelPrevSibling:
		if prev := prevElementSibling(el); prev != nil {
			return prev.TextContent()
		}
	case RelSectionHeading:
		return sectionHeading(el)
	}
	return ""
}

// classOverlap 返回录制 class 词元在候选 class 中的重合比例。
func classOverlap(recorded, candidate string) float64 {
	tokens := strings.Fields(recorded)
	if len(tokens) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, t := range strings.Fields(candidate) {
		set[t] = true
	}
	hit := 0
	for _, t := range tokens {
		if set[t] {
			hit++
		}
	}
	return float64(hit) / float64(len(tokens))
}
