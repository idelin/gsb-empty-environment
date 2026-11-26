package mxadmit

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PlanSafe classifies all deliveries and chooses the minimum number of fresh
// authentication receipts needed to make every MustDeliver delivery certain
// under every compatible ordering.  If no current plan exists, Feasible is
// false and Obligations lists the signature or authorization duties that
// remain unverified.
func PlanSafe(b Batch) (Plan, error) {
	m, err := buildModel(b, defaultOrderingLimit)
	if err != nil {
		return Plan{}, err
	}

	analyses := make([]deliveryAnalysis, len(b.Deliveries))
	for i, d := range b.Deliveries {
		analyses[i] = m.analyze(d)
	}

	var records []planRecord
	var unsatisfied []int
	for i := range analyses {
		if !analyses[i].Delivery.MustDeliver || analyses[i].OriginalSafe {
			continue
		}
		if !analyses[i].BaseOK || len(analyses[i].Pairs) == 0 {
			unsatisfied = append(unsatisfied, i)
			continue
		}
		records = append(records, planRecord{analysisIndex: i})
	}

	options, _ := m.buildReceiptOptions(analyses, records)
	selected, feasible := solveSetCover(len(records), options)

	p := Plan{
		Feasible:    feasible && len(unsatisfied) == 0,
		WorldCount:  len(m.worlds),
		Obligations: m.collectObligations(&analyses, unsatisfied),
	}

	assigned := make(map[int]int) // analysis index -> planned receipt index
	if feasible && len(unsatisfied) == 0 {
		for oi := range p.NewReceipts {
			_ = oi
		}
		for si, optIndex := range selected {
			opt := options[optIndex]
			pr := PlannedReceipt{
				ID:       fmt.Sprintf("new-%d", si+1),
				Endpoint: opt.endpoint,
				Commitment: Commitment{
					Domain:        opt.domain,
					MX:            opt.mx,
					PublicKey:     opt.key,
					PathDigest:    opt.path,
					DNSGeneration: opt.dns,
				},
			}
			minTick, maxTick := opt.from, opt.from
			first := true
			for ri := range records {
				if !maskHas(opt.mask, ri) {
					continue
				}
				ai := records[ri].analysisIndex
				if _, already := assigned[ai]; already {
					continue
				}
				assigned[ai] = si
				at := analyses[ai].Delivery.At
				if first || at < minTick {
					minTick = at
				}
				if first || at > maxTick {
					maxTick = at
				}
				first = false
				pr.DeliveryIDs = append(pr.DeliveryIDs, analyses[ai].Delivery.ID)
			}
			if len(pr.DeliveryIDs) == 0 {
				continue
			}
			sort.Strings(pr.DeliveryIDs)
			pr.Valid = Interval{From: minTick, To: maxTick}
			p.NewReceipts = append(p.NewReceipts, pr)
		}
		// The selected set-cover indices and assigned IDs must stay aligned.
		// Packing a receipt with no newly assigned records above would leave
		// a mismatch, so rebuild the mapping from the final receipts.
	}

	// Rebuild verdicts with the plan's receipt identifiers.
	verdictByAnalysis := make(map[int]Verdict, len(analyses))
	for i := range analyses {
		verdictByAnalysis[i] = analyses[i].verdict()
	}
	for pi := range p.NewReceipts {
		pr := &p.NewReceipts[pi]
		for _, id := range pr.DeliveryIDs {
			for ai := range analyses {
				if analyses[ai].Delivery.ID != id {
					continue
				}
				v := verdictByAnalysis[ai]
				v.Action = ActionRehandshake
				v.ReceiptID = pr.ID
				v.Commitment = pr.Commitment
				verdictByAnalysis[ai] = v
			}
		}
	}

	p.Verdicts = make([]Verdict, 0, len(analyses))
	for i := range analyses {
		p.Verdicts = append(p.Verdicts, verdictByAnalysis[i])
	}
	return p, nil
}

type planGroupKey struct {
	endpoint string
	domain   string
	mx       string
	dns      int64
}

type planRecord struct {
	analysisIndex int
}

type receiptOption struct {
	endpoint string
	domain   string
	mx       string
	dns      int64
	key      string
	path     string
	from     Tick
	to       Tick
	mask     []uint64
}

// buildReceiptOptions computes, for every pair that any delivery may use,
// the maximal contiguous safe intervals in which the pair is authorized by
// every compatible ordering.  A fresh receipt is one such pair over one such
// interval.
func (m *model) buildReceiptOptions(analyses []deliveryAnalysis, records []planRecord) ([]receiptOption, int) {
	groups := make(map[planGroupKey][]int)
	for ri := range records {
		a := &analyses[records[ri].analysisIndex]
		k := planGroupKey{
			endpoint: a.Endpoint,
			domain:   a.Domain,
			mx:       a.MX,
			dns:      a.DNSGeneration,
		}
		groups[k] = append(groups[k], ri)
	}

	keys := make([]planGroupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].endpoint != keys[j].endpoint {
			return keys[i].endpoint < keys[j].endpoint
		}
		if keys[i].domain != keys[j].domain {
			return keys[i].domain < keys[j].domain
		}
		if keys[i].mx != keys[j].mx {
			return keys[i].mx < keys[j].mx
		}
		return keys[i].dns < keys[j].dns
	})

	var options []receiptOption
	for _, gk := range keys {
		recordIndices := groups[gk]
		minTick, maxTick := Tick(math.MaxInt64), Tick(math.MinInt64)
		universe := make(map[[2]string]struct{})
		for _, ri := range recordIndices {
			a := &analyses[records[ri].analysisIndex]
			if a.Delivery.At < minTick {
				minTick = a.Delivery.At
			}
			if a.Delivery.At > maxTick {
				maxTick = a.Delivery.At
			}
			for pair := range a.Pairs {
				universe[pair] = struct{}{}
			}
		}
		if len(universe) == 0 {
			continue
		}
		pairs := make([][2]string, 0, len(universe))
		for pair := range universe {
			pairs = append(pairs, pair)
		}
		sort.Slice(pairs, func(i, j int) bool {
			if pairs[i][0] != pairs[j][0] {
				return pairs[i][0] < pairs[j][0]
			}
			return pairs[i][1] < pairs[j][1]
		})

		for _, pair := range pairs {
			intervals := m.pairSafeIntervals(gk, pair, minTick, maxTick, analyses)
			for _, in := range intervals {
				opt := receiptOption{
					endpoint: gk.endpoint,
					domain:   gk.domain,
					mx:       gk.mx,
					dns:      gk.dns,
					key:      pair[0],
					path:     pair[1],
					from:     in.From,
					to:       in.To,
				}
				for ri := range recordIndices {
					a := &analyses[records[recordIndices[ri]].analysisIndex]
					if a.Delivery.At < in.From || a.Delivery.At > in.To {
						continue
					}
					if _, ok := a.Pairs[pair]; !ok {
						continue
					}
					opt.mask = maskSet(opt.mask, recordIndices[ri], len(records))
				}
				if maskAny(opt.mask) {
					options = append(options, opt)
				}
			}
		}
	}
	return options, len(groups)
}

func (m *model) pairSafeIntervals(
	gk planGroupKey,
	pair [2]string,
	minTick, maxTick Tick,
	analyses []deliveryAnalysis,
) []Interval {
	points := []Tick{minTick}
	if maxTick < Tick(math.MaxInt64) {
		points = append(points, maxTick+1)
	}
	for _, a := range analyses {
		if a.Delivery.At >= minTick && a.Delivery.At <= maxTick {
			points = append(points, a.Delivery.At)
		}
	}
	for i := range m.events {
		e := &m.events[i]
		if e.At < minTick || e.At > maxTick {
			continue
		}
		if !eventApplies(e.Endpoint, gk.endpoint) {
			continue
		}
		points = append(points, e.At)
	}
	for i := range m.batch.Configs {
		c := &m.batch.Configs[i]
		if c.Validity.From >= minTick && c.Validity.From <= maxTick {
			points = append(points, c.Validity.From)
		}
		if c.Validity.To < Tick(math.MaxInt64) {
			if n := c.Validity.To + 1; n >= minTick && n <= maxTick {
				points = append(points, n)
			}
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	points = dedupeTicks(points)

	safeAt := func(t Tick) bool {
		return pairAllowed(m.certainPairs(gk.endpoint, gk.domain, gk.mx, gk.dns, t), pair[0], pair[1])
	}

	var out []Interval
	var cur *Interval
	for pi, start := range points {
		if start > maxTick {
			break
		}
		end := maxTick
		if pi+1 < len(points) {
			if points[pi+1]-1 < end {
				end = points[pi+1] - 1
			}
		}
		if start > end {
			continue
		}
		if safeAt(start) {
			if cur != nil && cur.To+1 == start {
				cur.To = end
			} else {
				in := Interval{From: start, To: end}
				cur = &in
				out = append(out, in)
			}
		} else {
			cur = nil
		}
	}
	// Merge adjacent segments split only by irrelevant validity boundaries.
	merged := out[:0]
	for _, in := range out {
		if len(merged) > 0 && merged[len(merged)-1].To+1 == in.From {
			merged[len(merged)-1].To = in.To
			continue
		}
		merged = append(merged, in)
	}
	return merged
}

func dedupeTicks(in []Tick) []Tick {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, t := range in[1:] {
		if t != out[len(out)-1] {
			out = append(out, t)
		}
	}
	return out
}

func maskWordCount(n int) int {
	return (n + 63) / 64
}

func maskSet(m []uint64, bit, n int) []uint64 {
	w := bit / 64
	if len(m) < maskWordCount(n) {
		nm := make([]uint64, maskWordCount(n))
		copy(nm, m)
		m = nm
	}
	m[w] |= uint64(1) << uint(bit%64)
	return m
}

func maskHas(m []uint64, bit int) bool {
	w := bit / 64
	return w < len(m) && m[w]&(uint64(1)<<uint(bit%64)) != 0
}

func maskAny(m []uint64) bool {
	for _, w := range m {
		if w != 0 {
			return true
		}
	}
	return false
}

func maskEmpty(m []uint64) bool {
	return !maskAny(m)
}

func maskCopy(m []uint64) []uint64 {
	out := make([]uint64, len(m))
	copy(out, m)
	return out
}

func maskWithout(a, b []uint64) []uint64 {
	out := make([]uint64, len(a))
	for i := range a {
		var bb uint64
		if i < len(b) {
			bb = b[i]
		}
		out[i] = a[i] & ^bb
	}
	return out
}

func maskAnd(a, b []uint64) []uint64 {
	out := make([]uint64, len(a))
	for i := range a {
		var bb uint64
		if i < len(b) {
			bb = b[i]
		}
		out[i] = a[i] & bb
	}
	return out
}

func maskFull(n int) []uint64 {
	m := make([]uint64, maskWordCount(n))
	for i := range m {
		m[i] = ^uint64(0)
	}
	if n%64 != 0 && len(m) > 0 {
		m[len(m)-1] = (uint64(1) << uint(n%64)) - 1
	}
	return m
}

func maskKey(m []uint64) string {
	var b strings.Builder
	for i, w := range m {
		if i > 0 {
			b.WriteByte('|')
		}
		fmt.Fprintf(&b, "%x", w)
	}
	return b.String()
}

func maskCount(m []uint64) int {
	n := 0
	for _, w := range m {
		n += popcount(w)
	}
	return n
}

func popcount(x uint64) int {
	// Portable implementation; the standard library's bits.OnesCount64 is
	// available, but this keeps the package dependency unchanged.
	x = x - ((x >> 1) & 0x5555555555555555)
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0F0F0F0F0F0F0F0F
	return int((x * 0x0101010101010101) >> 56)
}

type coverOption struct {
	mask []uint64
	idx  int
}

// solveSetCover returns indices of options forming a minimum cover of all n
// records.  The batch sizes in this package are intended to be small enough
// for an exact branch-and-bound search; large batches still terminate but can
// take longer.
func solveSetCover(n int, options []receiptOption) ([]int, bool) {
	if n == 0 {
		return nil, true
	}
	full := maskFull(n)
	var opts []coverOption
	for i := range options {
		if !maskAny(options[i].mask) {
			continue
		}
		// Normalize the mask to the same word count as the universe.
		m := make([]uint64, maskWordCount(n))
		copy(m, options[i].mask)
		opts = append(opts, coverOption{mask: m, idx: i})
	}
	// Deduplicate identical coverage masks, preferring the option with a
	// wider interval (deterministically ordered by index).
	seen := make(map[string]int)
	kept := make([]coverOption, 0, len(opts))
	for _, o := range opts {
		k := maskKey(o.mask)
		if prev, ok := seen[k]; ok {
			a, b := &kept[prev], o
			if b.idx < a.idx {
				a.idx = b.idx
			}
			continue
		}
		seen[k] = len(kept)
		kept = append(kept, o)
	}
	opts = kept

	optsFor := make([][]int, n)
	for oi := range opts {
		for bit := 0; bit < n; bit++ {
			if maskHas(opts[oi].mask, bit) {
				optsFor[bit] = append(optsFor[bit], oi)
			}
		}
	}
	for bit := 0; bit < n; bit++ {
		if len(optsFor[bit]) == 0 {
			return nil, false
		}
	}
	sort.Slice(opts, func(i, j int) bool {
		ci, cj := maskCount(opts[i].mask), maskCount(opts[j].mask)
		if ci != cj {
			return ci > cj
		}
		return opts[i].idx < opts[j].idx
	})
	optsFor = make([][]int, n)
	for oi := range opts {
		for bit := 0; bit < n; bit++ {
			if maskHas(opts[oi].mask, bit) {
				optsFor[bit] = append(optsFor[bit], oi)
			}
		}
	}

	best := n + 1
	var bestSel []int
	seenDepth := make(map[string]int)
	var dfs func(rem []uint64, sel []int) 
	dfs = func(rem []uint64, sel []int) {
		if maskEmpty(rem) {
			if len(sel) < best {
				best = len(sel)
				bestSel = append([]int(nil), sel...)
			}
			return
		}
		if len(sel) >= best {
			return
		}
		k := maskKey(rem)
		if d, ok := seenDepth[k]; ok && d <= len(sel) {
			return
		}
		seenDepth[k] = len(sel)

		// Choose the uncovered record with the fewest remaining options.
		chosen := -1
		for bit := 0; bit < n; bit++ {
			if !maskHas(rem, bit) {
				continue
			}
			if chosen == -1 || len(optsFor[bit]) < len(optsFor[chosen]) {
				chosen = bit
			}
		}

		for _, oi := range optsFor[chosen] {
			next := maskWithout(rem, opts[oi].mask)
			dfs(next, append(sel, oi))
		}
	}
	dfs(full, nil)
	if best > n {
		return nil, false
	}
	out := make([]int, len(bestSel))
	for i, oi := range bestSel {
		out[i] = opts[oi].idx
	}
	return out, true
}

func (m *model) collectObligations(analyses *[]deliveryAnalysis, unsatisfied []int) []Obligation {
	var out []Obligation
	seen := make(map[string]struct{})
	for _, ai := range unsatisfied {
		a := &(*analyses)[ai]
		req := Commitment{
			Domain:        a.Domain,
			MX:            a.MX,
			PublicKey:     a.KeyRestriction,
			PathDigest:    a.PathRestriction,
			DNSGeneration: a.DNSGeneration,
		}
		if req.Domain == "" {
			req.Domain = a.Delivery.Domain
		}
		for i := range m.batch.Configs {
			c := &m.batch.Configs[i]
			if req.Domain != "" && !c.MentionsDomain(req.Domain) {
				continue
			}
			activeCount := 0
			for wi := range m.worlds {
				if m.authorityAt(&m.worlds[wi], a.Endpoint, req.Domain, a.Delivery.At) == c {
					activeCount++
				}
			}
			hasDNSTuple := false
			for _, e := range c.Entries {
				if e.Domain == req.Domain && e.MX == req.MX && e.DNSGeneration == req.DNSGeneration {
					hasDNSTuple = true
					break
				}
			}
			reason := ""
			switch {
			case !c.Validity.Contains(a.Delivery.At):
				reason = fmt.Sprintf("signature validity %s does not cover tick %d", c.Validity, int64(a.Delivery.At))
			case activeCount == 0:
				reason = "signature activation is not observed as the domain authority at the delivery tick"
			case activeCount < len(m.worlds):
				reason = "signature activation is not established in every compatible ordering"
			case !hasDNSTuple:
				reason = "configuration does not jointly authorize the required MX and DNS generation for this domain"
			default:
				reason = "public key and certificate path are not jointly authorized with the required DNS facts"
			}
			ob := Obligation{
				DeliveryID: a.Delivery.ID,
				ConfigID:   c.ID,
				Signer:     c.Signer,
				Validity:   c.Validity,
				At:         a.Delivery.At,
				Required:   req,
				Reason:     reason,
			}
			k := fmt.Sprintf("%s|%s|%s", ob.DeliveryID, ob.ConfigID, ob.Reason)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, ob)
		}
		if len(out) == 0 || !hasObligationFor(out, a.Delivery.ID) {
			ob := Obligation{
				DeliveryID: a.Delivery.ID,
				At:         a.Delivery.At,
				Required:   req,
				Reason:     "no signed configuration mentions the required domain and DNS authorization",
			}
			k := fmt.Sprintf("%s||%s", ob.DeliveryID, ob.Reason)
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, ob)
			}
		}
	}
	return out
}

func hasObligationFor(obs []Obligation, deliveryID string) bool {
	for _, o := range obs {
		if o.DeliveryID == deliveryID {
			return true
		}
	}
	return false
}
