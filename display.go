package displaycommit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

type Line struct {
	Label string `json:"label,omitempty"`
	Text  string `json:"text"`
	Value string `json:"value,omitempty"`

	Role  Role     `json:"role,omitempty"`
	Paths []string `json:"paths,omitempty"`

	Asset    string   `json:"asset,omitempty"`
	Amount   *big.Int `json:"amount,omitempty"`
	Decimals int      `json:"decimals,omitempty"`
	Interval Interval `json:"interval,omitempty"`

	Merged      bool     `json:"merged,omitempty"`
	SourcePaths []string `json:"source_paths,omitempty"`
	Defaulted   bool     `json:"defaulted,omitempty"`
}

type PageRef struct {
	Index int    `json:"index"`
	Label string `json:"label,omitempty"`
	Hash  Hash   `json:"hash,omitempty"`
}

type Page struct {
	Index  int    `json:"index"`
	Number int    `json:"number,omitempty"`
	Title  string `json:"title,omitempty"`
	Lines  []Line `json:"lines,omitempty"`

	PreviewPage *int      `json:"preview_page,omitempty"`
	References  []PageRef `json:"references,omitempty"`

	// Proof is optional and is not part of the visible screen key.
	Proof *PageProof `json:"proof,omitempty"`
}

// Screen is descriptive terminology for a Page.
type Screen = Page

type Display struct {
	Title string `json:"title,omitempty"`
	Pages []Page `json:"pages"`

	// VisibleHash commits only to ordered visible content. It deliberately
	// excludes hidden path/proof metadata.
	VisibleHash Hash `json:"visible_hash,omitempty"`
}

type FieldRule struct {
	Path  any    `json:"path"`
	Label string `json:"label,omitempty"`
	Role  Role   `json:"role,omitempty"`

	Format string `json:"format,omitempty"` // exact|decimal|short-address|safe-text|hex

	OmitDefault *bool         `json:"omit_default,omitempty"`
	Rounding    *RoundingRule `json:"rounding,omitempty"`
	AssetPath   any           `json:"asset_path,omitempty"`

	ShowInterval bool `json:"show_interval,omitempty"`
}

type PageRule struct {
	Title  string      `json:"title,omitempty"`
	Fields []FieldRule `json:"fields,omitempty"`

	// PreviewPage references an earlier summary/preview page by zero-based
	// index. Negative or nil means no reference.
	PreviewPage *int `json:"preview_page,omitempty"`
	Preview     any  `json:"preview,omitempty"`
}

type DisplayRule struct {
	Name    string `json:"name,omitempty"`
	Version any    `json:"version,omitempty"`

	OmitDefaults      bool `json:"omit_defaults,omitempty"`
	MergeSameAsset    bool `json:"merge_same_asset,omitempty"`
	MergeAmounts      bool `json:"merge_amounts,omitempty"`
	ShowAmountRange   bool `json:"show_amount_range,omitempty"`
	UseClosedInterval bool `json:"use_closed_interval,omitempty"`

	TextPolicy *TextPolicy `json:"text_policy,omitempty"`

	Pages []PageRule `json:"pages,omitempty"`

	// NonAuthorizedFields is the host's explicit malleability declaration.
	// Entries may be Path, string, []int, or int.
	NonAuthorizedFields []any `json:"non_authorized_fields,omitempty"`
	MalleableFields     []any `json:"malleable_fields,omitempty"`
	UnauthorizedFields  []any `json:"unauthorized_fields,omitempty"`

	// RequireExactCommitments means rounded/summarized lines must still have
	// inclusion commitments bound into the signing request.
	RequireExactCommitments bool `json:"require_exact_commitments,omitempty"`
}

func (r DisplayRule) mergeEnabled() bool {
	return r.MergeSameAsset || r.MergeAmounts
}

func (r DisplayRule) nonAuthorizedNormalized() ([]Path, error) {
	raw := append([]any(nil), r.NonAuthorizedFields...)
	raw = append(raw, r.MalleableFields...)
	raw = append(raw, r.UnauthorizedFields...)
	var out []Path
	for _, item := range raw {
		p, err := NormalizePath(item)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// VisibleKey returns the canonical ordered screen content used by the
// small-domain comparator.
func (d Display) VisibleKey() string {
	var b strings.Builder
	for pi, p := range d.Pages {
		fmt.Fprintf(&b, "page:%d:%s\n", pi, p.Title)
		for _, l := range p.Lines {
			fmt.Fprintf(&b, "line:%s=%s\n", l.Label, l.Text)
		}
		for _, ref := range p.References {
			fmt.Fprintf(&b, "ref:%d:%s\n", ref.Index, ref.Label)
		}
		if p.PreviewPage != nil {
			fmt.Fprintf(&b, "preview:%d\n", *p.PreviewPage)
		}
	}
	return b.String()
}

// VisualKey models pixels seen by the user: invisible runes vanish and known
// confusables collapse to a common skeleton. Safe rendering adds explicit
// code-point annotations, so its VisualKey remains collision-free.
func (d Display) VisualKey() string {
	var b strings.Builder
	for pi, p := range d.Pages {
		fmt.Fprintf(&b, "page:%d:%s\n", pi, VisualSkeleton(p.Title))
		for _, l := range p.Lines {
			fmt.Fprintf(&b, "line:%s=%s\n", VisualSkeleton(l.Label), VisualSkeleton(l.Text))
		}
		for _, ref := range p.References {
			fmt.Fprintf(&b, "ref:%d:%s\n", ref.Index, VisualSkeleton(ref.Label))
		}
		if p.PreviewPage != nil {
			fmt.Fprintf(&b, "preview:%d\n", *p.PreviewPage)
		}
	}
	return b.String()
}

func (d *Display) computeVisibleHash() Hash {
	h := sha256.Sum256([]byte(d.VisibleKey()))
	return Hash(h)
}

type amountBinding struct {
	Path      Path
	Payee     string
	PayeePath string
	Asset     string
	Value     *big.Int
	Decimals  int
}

// Render builds ordered pages for one message.
func Render(table *TypeTable, msg *Message, rule *DisplayRule) (*Display, error) {
	if msg == nil {
		return nil, fmt.Errorf("displaycommit: nil message")
	}
	if rule == nil {
		rule = &DisplayRule{}
	}
	policy := rule.TextPolicy
	if policy == nil {
		dp := DefaultTextPolicy()
		policy = &dp
	}

	pageRules := rule.Pages
	if len(pageRules) == 0 {
		pageRules = automaticPageRules(table, msg)
	}

	amounts := collectAmounts(table, msg, rule)
	mergedLines := map[string]Line{}
	var mergedOrder []string
	if rule.mergeEnabled() {
		groups := map[string][]amountBinding{}
		for _, a := range amounts {
			key := strings.ToUpper(strings.TrimSpace(a.Asset)) + "#" + fmt.Sprint(a.Decimals)
			if _, ok := groups[key]; !ok {
				mergedOrder = append(mergedOrder, key)
			}
			groups[key] = append(groups[key], a)
		}
		sort.Strings(mergedOrder)
		for _, key := range mergedOrder {
			group := groups[key]
			total := big.NewInt(0)
			var iv Interval
			var paths []string
			asset := ""
			decimals := 0
			vals := make([]*big.Int, 0, len(group))
			for _, a := range group {
				total.Add(total, a.Value)
				vals = append(vals, a.Value)
				paths = append(paths, a.Path.Key())
				asset = a.Asset
				decimals = a.Decimals
			}
			iv = IntervalOfValues(vals...)
			line := Line{
				Label:       "Total " + asset,
				Text:        FormatDecimal(total, decimals) + " " + asset,
				Value:       total.String(),
				Role:        RoleAmount,
				Asset:       asset,
				Amount:      total,
				Decimals:    decimals,
				Interval:    iv,
				Merged:      true,
				SourcePaths: paths,
			}
			if rule.ShowAmountRange || rule.UseClosedInterval {
				line.Text += " range" + iv.String()
			}
			mergedLines[key] = line
		}
	}

	out := &Display{}
	for i, pr := range pageRules {
		page := Page{Index: i, Number: i + 1, Title: pr.Title}
		if pr.PreviewPage != nil {
			n := *pr.PreviewPage
			page.PreviewPage = &n
			page.References = append(page.References, PageRef{Index: n, Label: "preview"})
		} else if pr.Preview != nil {
			if v, ok := pr.Preview.(int); ok {
				page.PreviewPage = &v
				page.References = append(page.References, PageRef{Index: v, Label: "preview"})
			} else if s, ok := pr.Preview.(string); ok {
				var idx int
				if _, err := fmt.Sscanf(s, "%d", &idx); err == nil {
					page.PreviewPage = &idx
					page.References = append(page.References, PageRef{Index: idx, Label: "preview"})
				}
			}
		}

		for _, fr := range pr.Fields {
			rs, err := msg.Resolve(fr.Path)
			if err != nil {
				return nil, err
			}
			for _, r := range rs {
				spec, _ := specForResolved(table, msg.typeName(), r)
				omitDefault := rule.OmitDefaults
				if fr.OmitDefault != nil {
					omitDefault = *fr.OmitDefault
				}
				if omitDefault && defaultValueEqual(spec, r.Field) {
					continue
				}
				if rule.mergeEnabled() && lineIsAmount(fr, spec) {
					continue
				}
				line, err := renderLine(r, fr, spec, policy)
				if err != nil {
					return nil, err
				}
				page.Lines = append(page.Lines, line)
			}
		}

		if rule.mergeEnabled() && pageRuleWantsMerged(pr) {
			for _, key := range mergedOrder {
				page.Lines = append(page.Lines, mergedLines[key])
			}
		}
		out.Pages = append(out.Pages, page)
	}
	out.VisibleHash = out.computeVisibleHash()
	return out, nil
}

func pageRuleWantsMerged(pr PageRule) bool {
	for _, f := range pr.Fields {
		if f.Role == RoleAmount {
			return true
		}
	}
	return len(pr.Fields) == 0
}

func lineIsAmount(fr FieldRule, spec FieldSpec) bool {
	return fr.Role == RoleAmount || spec.hasRole(RoleAmount) || fr.Format == "amount"
}

func automaticPageRules(table *TypeTable, msg *Message) []PageRule {
	var fields []FieldRule
	seen := map[string]bool{}
	add := func(path Path, role Role, label string) {
		key := path.Key()
		if seen[key] {
			return
		}
		seen[key] = true
		fields = append(fields, FieldRule{Path: path, Role: role, Label: label})
	}
	if table != nil {
		for _, role := range []Role{RolePayee, RoleAsset, RoleAmount, RoleDomain, RoleDeadline} {
			for _, p := range table.RolePaths(role) {
				add(p, role, string(role))
			}
		}
	}
	if len(fields) == 0 {
		for _, l := range msg.Leaves() {
			add(l.Path, "", fmt.Sprintf("f%d", l.FieldNumber))
		}
	}
	return []PageRule{{Title: "Transaction", Fields: fields}}
}

func specForResolved(table *TypeTable, typeName string, r Resolved) (FieldSpec, bool) {
	if table == nil || len(r.Path) == 0 {
		return FieldSpec{}, false
	}
	return table.SpecAtPath(typeName, r.Path)
}

func renderLine(r Resolved, fr FieldRule, spec FieldSpec, policy *TextPolicy) (Line, error) {
	label := fr.Label
	if label == "" {
		label = spec.Name
	}
	if label == "" {
		label = fmt.Sprintf("Field %d", r.Path[len(r.Path)-1].Field)
	}
	role := fr.Role
	if role == "" {
		for _, rr := range spec.allRoles() {
			if rr != RoleNonAuth {
				role = rr
			}
		}
	}
	line := Line{Label: label, Role: role, Paths: []string{r.Path.Key()}}

	format := fr.Format
	if format == "" {
		format = spec.Format
	}
	switch {
	case role == RoleAmount || format == "amount" || spec.Kind == KindInt && spec.hasRole(RoleAmount):
		v := valueInt(r.Field.Value)
		decimals := spec.Decimals
		line.Amount = v
		line.Decimals = decimals
		line.Asset = ""
		if fr.Rounding != nil {
			rounded, err := RoundBigInt(v, *fr.Rounding)
			if err != nil {
				return line, err
			}
			line.Text = FormatDecimal(rounded, decimals)
			iv, err := RoundingInterval(rounded, *fr.Rounding)
			if err == nil {
				line.Interval = iv
			}
		} else {
			line.Text = FormatDecimal(v, decimals)
		}
		line.Value = v.String()
	case r.Value.Kind == KindAddress || format == "address" || format == "short-address":
		addr := r.Value.Address
		text := addr.Text
		if text == "" && len(addr.Bytes) > 0 {
			text = hex.EncodeToString(addr.Bytes)
		}
		if format == "short-address" && len([]rune(text)) > 12 {
			rs := []rune(text)
			text = string(rs[:6]) + "…" + string(rs[len(rs)-4:])
		}
		safe, err := InspectRestrictedText(text, policy)
		if err != nil {
			return line, err
		}
		line.Text = safe.Display
		line.Value = addr.canonical()
	case r.Value.Kind == KindText || format == "safe-text" || format == "text":
		safe, err := InspectRestrictedText(r.Value.Text, policy)
		if err != nil {
			return line, err
		}
		line.Text = safe.Display
		line.Value = r.Value.Text
	case r.Value.Kind == KindInt || spec.Kind == KindInt:
		v := valueInt(r.Field.Value)
		if fr.Rounding != nil {
			rounded, err := RoundBigInt(v, *fr.Rounding)
			if err != nil {
				return line, err
			}
			line.Text = rounded.String()
		} else {
			line.Text = v.String()
		}
		line.Value = v.String()
	case r.Value.Kind == KindBool:
		line.Text = fmt.Sprint(r.Value.Bool)
		line.Value = line.Text
	case r.Value.Kind == KindBytes:
		line.Text = hex.EncodeToString(r.Value.Bytes)
		line.Value = line.Text
	default:
		b, _ := json.Marshal(r.Value)
		line.Text = string(b)
		line.Value = line.Text
	}
	return line, nil
}

func collectAmounts(table *TypeTable, msg *Message, rule *DisplayRule) []amountBinding {
	var amountPaths []Path
	if table != nil {
		amountPaths = append(amountPaths, table.RolePaths(RoleAmount)...)
	}
	// Also infer from leaves/specs if table role paths miss nested values.
	if len(amountPaths) == 0 {
		for _, l := range msg.Leaves() {
			if table != nil {
				if spec, ok := table.fieldSpec(msg.typeName(), l.FieldNumber); ok && spec.hasRole(RoleAmount) {
					amountPaths = append(amountPaths, l.Path)
				}
			}
		}
	}
	assetPaths := []Path{}
	payeePaths := []Path{}
	if table != nil {
		assetPaths = table.RolePaths(RoleAsset)
		payeePaths = table.RolePaths(RolePayee)
	}

	var out []amountBinding
	for _, ap := range amountPaths {
		rs, err := msg.Resolve(ap)
		if err != nil || len(rs) == 0 {
			continue
		}
		for _, r := range rs {
			v := valueInt(r.Value)
			if v == nil {
				continue
			}
			asset := nearestScalar(msg, r.Path, assetPaths)
			payee, payeePath := nearestBinding(msg, r.Path, payeePaths)
			decimals := 0
			if table != nil {
				if spec, ok := table.SpecAtPath(msg.typeName(), r.Path); ok {
					decimals = spec.Decimals
				}
			}
			out = append(out, amountBinding{Path: r.Path, Payee: payee, PayeePath: payeePath, Asset: asset, Value: v, Decimals: decimals})
		}
	}
	return out
}

func firstField(p Path) int {
	if len(p) == 0 {
		return 0
	}
	return p[0].Field
}

func pathPrefixLen(a, b Path) int {
	n := 0
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i].Field != b[i].Field || a[i].Index != b[i].Index {
			// Index -1 means "any ordinal" in table-generated paths.
			if a[i].Field != b[i].Field {
				break
			}
			if a[i].Index >= 0 && b[i].Index >= 0 && a[i].Index != b[i].Index {
				break
			}
		}
		n++
	}
	return n
}

func nearestScalar(msg *Message, target Path, candidates []Path) string {
	best := ""
	bestLen := -1
	for _, cp := range candidates {
		rs, err := msg.Resolve(cp)
		if err != nil || len(rs) == 0 {
			continue
		}
		l := pathPrefixLen(target, cp)
		if l > bestLen {
			bestLen = l
			r := rs[0]
			if r.Value.Kind == KindAddress {
				best = r.Value.Address.Text
			} else {
				best = r.Value.Text
				if best == "" && r.Value.Int != nil {
					best = r.Value.Int.String()
				}
			}
		}
	}
	return best
}

func nearestBinding(msg *Message, target Path, candidates []Path) (string, string) {
	best := ""
	bestPath := ""
	bestLen := -1
	for _, cp := range candidates {
		rs, err := msg.Resolve(cp)
		if err != nil || len(rs) == 0 {
			continue
		}
		l := pathPrefixLen(target, cp)
		if l > bestLen {
			bestLen = l
			r := rs[0]
			if r.Value.Kind == KindAddress {
				best = r.Value.Address.canonical()
			} else {
				best = r.Value.Text
			}
			bestPath = cp.Key()
		}
	}
	return best, bestPath
}

// MarshalVisibleJSON is a helper for stable tests.
func (d Display) MarshalVisibleJSON() ([]byte, error) {
	type visibleLine struct {
		Label string `json:"label"`
		Text  string `json:"text"`
	}
	type visiblePage struct {
		Index int           `json:"index"`
		Title string        `json:"title"`
		Lines []visibleLine `json:"lines"`
	}
	var v struct {
		Pages []visiblePage `json:"pages"`
	}
	for _, p := range d.Pages {
		vp := visiblePage{Index: p.Index, Title: p.Title}
		for _, l := range p.Lines {
			vp.Lines = append(vp.Lines, visibleLine{Label: l.Label, Text: l.Text})
		}
		v.Pages = append(v.Pages, vp)
	}
	return json.Marshal(v)
}

var _ = hex.EncodeToString
