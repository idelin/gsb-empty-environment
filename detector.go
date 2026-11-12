package taskpkg

import (
	"bytes"
	"encoding/hex"
	"sort"
	"time"
)

type queryRecord struct {
	id string
	at time.Time
}

type variantState struct {
	dataHex string
	data    []byte
	records []queryRecord
}

type fragmentState struct {
	index    uint16
	variants map[string]*variantState
}

type windowKey struct {
	client             string
	registeredDomain   string
	startNano, endNano int64
}

func makeWindowKey(client, registeredDomain string, w Window) windowKey {
	return windowKey{
		client:           client,
		registeredDomain: registeredDomain,
		startNano:        w.Start.UnixNano(),
		endNano:          w.End.UnixNano(),
	}
}

type windowState struct {
	key       windowKey
	window    Window
	fragments map[uint16]*fragmentState
}

func newWindowState(key windowKey, w Window) *windowState {
	return &windowState{
		key:       key,
		window:    w,
		fragments: make(map[uint16]*fragmentState),
	}
}

// Detector accumulates DNS queries into windows and emits alerts when a
// window is sealed. It is not safe for concurrent use.
type Detector struct {
	cfg Config

	defaultWindow Window
	hasDefault    bool
	watermark     time.Time

	windows map[windowKey]*windowState
	sealed  map[windowKey]struct{}
}

// New returns a detector configured with K distinct-fragment and B byte
// thresholds.
func New(k, b int) *Detector {
	return newDetector(Config{K: k, B: b})
}

// Engine and StreamingDetector are aliases for Detector.
type Engine = Detector
type StreamingDetector = Detector

// NewDetector creates a detector. It accepts either Config or (K, B) integer
// arguments. This keeps the construction convenient for probes while allowing
// a descriptive Config.
func NewDetector(args ...any) *Detector {
	switch len(args) {
	case 0:
		return newDetector(Config{})
	case 1:
		switch cfg := args[0].(type) {
		case Config:
			return newDetector(cfg)
		case *Config:
			if cfg != nil {
				return newDetector(*cfg)
			}
		}
	case 2:
		k, okK := args[0].(int)
		b, okB := args[1].(int)
		if okK && okB {
			return newDetector(Config{K: k, B: b})
		}
	}
	panic("taskpkg: NewDetector expects Config or (K, B)")
}

// NewDetectorWithConfig creates a detector from Config.
func NewDetectorWithConfig(cfg Config) *Detector {
	return newDetector(cfg)
}

// NewEngine and NewStream are aliases for NewDetector.
func NewEngine(args ...any) *Detector { return NewDetector(args...) }
func NewStream(args ...any) *Detector { return NewDetector(args...) }

func newDetector(cfg Config) *Detector {
	cfg = cfg.withDefaults()
	d := &Detector{
		cfg:     cfg,
		windows: make(map[windowKey]*windowState),
		sealed:  make(map[windowKey]struct{}),
	}
	if cfg.Window != nil && cfg.Window.Valid() {
		d.defaultWindow = *cfg.Window
		d.hasDefault = true
	} else if cfg.Start.Before(cfg.End) {
		d.defaultWindow = Window{Start: cfg.Start, End: cfg.End}
		d.hasDefault = true
	}
	return d
}

func (d *Detector) ensure() {
	if d.windows == nil {
		d.windows = make(map[windowKey]*windowState)
	}
	if d.sealed == nil {
		d.sealed = make(map[windowKey]struct{})
	}
	d.cfg = d.cfg.withDefaults()
}

// Config returns the effective thresholds and default window.
func (d *Detector) Config() Config {
	cfg := d.cfg.withDefaults()
	if d.hasDefault {
		w := d.defaultWindow
		cfg.Window = &w
	}
	return cfg
}

// SetWindow sets the default window for subsequent queries. A new window does
// not implicitly seal older windows; callers should advance the watermark or
// call CloseWindow for deterministic finalization.
func (d *Detector) SetWindow(args ...any) {
	d.ensure()
	if w, ok := parseWindowArgs(args); ok {
		d.defaultWindow = w
		d.hasDefault = true
	}
}

// Observe feeds one query to the detector. An optional Window, or a pair of
// start/end times, overrides the query's own window and the detector default.
//
// Alerts are emitted only when the window is sealed by the watermark, CloseWindow
// or CloseAll. The returned slice contains alerts finalized by this call and is
// normally empty.
func (d *Detector) Observe(q Query, args ...any) []Alert {
	d.ensure()
	var w Window
	var ok bool
	if len(args) > 0 {
		w, ok = parseWindowArgs(args)
	} else {
		w, ok = q.explicitWindow()
		if !ok && d.hasDefault {
			w, ok = d.defaultWindow, d.defaultWindow.Valid()
		}
	}
	if !ok || !w.Valid() {
		return nil
	}

	ts := q.timestamp()
	if ts.IsZero() {
		ts = w.Start
	}
	if !w.Contains(ts) {
		return nil
	}

	label, domain, ok := payloadLabel(q)
	if !ok {
		return nil
	}
	index, data, ok := ParseHexLabel(label)
	if !ok {
		return nil
	}
	key := makeWindowKey(q.clientID(), domain, w)

	// A window ending at or before the watermark is complete. Events that
	// arrive for such a window are late and must not reopen it.
	if _, closed := d.sealed[key]; closed {
		return nil
	}
	if !d.watermark.IsZero() && !w.End.After(d.watermark) {
		return nil
	}

	ws := d.windows[key]
	if ws == nil {
		ws = newWindowState(key, w)
		d.windows[key] = ws
	}
	fs := ws.fragments[index]
	if fs == nil {
		fs = &fragmentState{
			index:    index,
			variants: make(map[string]*variantState),
		}
		ws.fragments[index] = fs
	}

	dataHex := hex.EncodeToString(data)
	v := fs.variants[dataHex]
	if v == nil {
		v = &variantState{
			dataHex: dataHex,
			data:    append([]byte(nil), data...),
		}
		fs.variants[dataHex] = v
	}
	v.records = append(v.records, queryRecord{id: q.queryID(), at: ts})
	return nil
}

// Add, Feed, Ingest, Process and AddEvent are aliases for Observe.
func (d *Detector) Add(q Query, args ...any) []Alert      { return d.Observe(q, args...) }
func (d *Detector) Feed(q Query, args ...any) []Alert     { return d.Observe(q, args...) }
func (d *Detector) Ingest(q Query, args ...any) []Alert   { return d.Observe(q, args...) }
func (d *Detector) Process(q Query, args ...any) []Alert  { return d.Observe(q, args...) }
func (d *Detector) AddEvent(q Query, args ...any) []Alert { return d.Observe(q, args...) }
func (d *Detector) Push(q Query, args ...any) []Alert     { return d.Observe(q, args...) }
func (d *Detector) OnQuery(q Query, args ...any) []Alert  { return d.Observe(q, args...) }
func (d *Detector) Record(q Query, args ...any) []Alert   { return d.Observe(q, args...) }
func (d *Detector) Handle(q Query, args ...any) []Alert   { return d.Observe(q, args...) }
func (d *Detector) Detect(q Query, args ...any) []Alert   { return d.Observe(q, args...) }

// ObserveWindow feeds a query using an explicit half-open window.
func (d *Detector) ObserveWindow(q Query, w Window) []Alert {
	return d.Observe(q, w)
}

// AddToWindow, FeedWindow and ObserveInWindow are aliases for ObserveWindow.
func (d *Detector) AddToWindow(q Query, w Window) []Alert     { return d.Observe(q, w) }
func (d *Detector) FeedWindow(q Query, w Window) []Alert      { return d.Observe(q, w) }
func (d *Detector) ObserveInWindow(q Query, w Window) []Alert { return d.Observe(q, w) }

// AdvanceWatermark seals every window whose end is at or before t and returns
// the alerts produced by those windows. Once sealed, a window cannot accept
// late events.
func (d *Detector) AdvanceWatermark(t time.Time) []Alert {
	d.ensure()
	if d.watermark.IsZero() || t.After(d.watermark) {
		d.watermark = t
	}
	return d.closeMatching(func(ws *windowState) bool {
		return !ws.window.End.After(t)
	})
}

// Watermark, At and Seal are aliases for AdvanceWatermark.
func (d *Detector) Watermark(t time.Time) []Alert { return d.AdvanceWatermark(t) }
func (d *Detector) At(t time.Time) []Alert        { return d.AdvanceWatermark(t) }
func (d *Detector) Seal(t time.Time) []Alert      { return d.AdvanceWatermark(t) }
func (d *Detector) Advance(t time.Time) []Alert   { return d.AdvanceWatermark(t) }
func (d *Detector) Flush(t ...time.Time) []Alert {
	if len(t) > 0 {
		return d.AdvanceWatermark(t[0])
	}
	return d.CloseAll()
}

// CloseWindow seals windows with the given half-open bounds and returns their
// alerts. It accepts either a Window or (start, end) time arguments.
func (d *Detector) CloseWindow(args ...any) []Alert {
	d.ensure()
	w, ok := parseWindowArgs(args)
	if !ok {
		return nil
	}
	return d.closeMatching(func(ws *windowState) bool {
		return ws.window.Start.Equal(w.Start) && ws.window.End.Equal(w.End)
	})
}

// CloseAll seals every open window and returns all alerts.
func (d *Detector) CloseAll() []Alert {
	d.ensure()
	return d.closeMatching(func(*windowState) bool { return true })
}

// Finish is an alias for CloseAll.
func (d *Detector) Finish() []Alert { return d.CloseAll() }

func (d *Detector) closeMatching(match func(*windowState) bool) []Alert {
	var alerts []Alert
	var keys []windowKey
	for key, ws := range d.windows {
		if match(ws) {
			keys = append(keys, key)
			if alert := buildAlert(ws, d.cfg); alert != nil {
				alerts = append(alerts, *alert)
			}
		}
	}
	for _, key := range keys {
		delete(d.windows, key)
		d.sealed[key] = struct{}{}
	}
	sortAlerts(alerts)
	return alerts
}

func sortAlerts(alerts []Alert) {
	sort.Slice(alerts, func(i, j int) bool {
		a, b := alerts[i], alerts[j]
		if !a.Window.End.Equal(b.Window.End) {
			return a.Window.End.Before(b.Window.End)
		}
		if !a.Window.Start.Equal(b.Window.Start) {
			return a.Window.Start.Before(b.Window.Start)
		}
		if a.Client != b.Client {
			return a.Client < b.Client
		}
		return a.RegisteredDomain < b.RegisteredDomain
	})
}

type candidateVariant struct {
	dataHex string
	data    []byte
	records []queryRecord
}

func canonicalCandidate(v *variantState) candidateVariant {
	records := sortedRecords(v.records)
	return candidateVariant{
		dataHex: v.dataHex,
		data:    append([]byte(nil), v.data...),
		records: records,
	}
}

func variantLess(a, b candidateVariant) bool {
	if len(a.records) == 0 || len(b.records) == 0 {
		return a.dataHex < b.dataHex
	}
	ra, rb := a.records[0], b.records[0]
	if !ra.at.Equal(rb.at) {
		return ra.at.Before(rb.at)
	}
	if ra.id != rb.id {
		return ra.id < rb.id
	}
	return a.dataHex < b.dataHex
}

func sortedRecords(records []queryRecord) []queryRecord {
	out := append([]queryRecord(nil), records...)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].at.Equal(out[j].at) {
			return out[i].at.Before(out[j].at)
		}
		return out[i].id < out[j].id
	})
	return out
}

func uniqueQueryIDs(records []queryRecord) []string {
	records = sortedRecords(records)
	seen := make(map[string]struct{}, len(records))
	ids := make([]string, 0, len(records))
	for _, r := range records {
		if r.id == "" {
			continue
		}
		if _, ok := seen[r.id]; ok {
			continue
		}
		seen[r.id] = struct{}{}
		ids = append(ids, r.id)
	}
	return ids
}

func buildFragment(fs *fragmentState) (FragmentEvidence, []Conflict, candidateVariant) {
	candidates := make([]candidateVariant, 0, len(fs.variants))
	for _, v := range fs.variants {
		candidates = append(candidates, canonicalCandidate(v))
	}
	sort.Slice(candidates, func(i, j int) bool {
		return variantLess(candidates[i], candidates[j])
	})

	canonical := candidates[0]
	canonicalID := ""
	if len(canonical.records) > 0 {
		canonicalID = canonical.records[0].id
	}
	ev := FragmentEvidence{
		Index:      fs.index,
		DataHex:    canonical.dataHex,
		Hex:        canonical.dataHex,
		Data:       append([]byte(nil), canonical.data...),
		ByteLen:    len(canonical.data),
		QueryID:    canonicalID,
		QueryIDs:   uniqueQueryIDs(canonical.records),
		Duplicates: duplicateCount(canonical.records),
	}

	var conflicts []Conflict
	for _, c := range candidates[1:] {
		id := ""
		if len(c.records) > 0 {
			id = c.records[0].id
		}
		conflicts = append(conflicts, Conflict{
			Index:            fs.index,
			DataHex:          c.dataHex,
			Hex:              c.dataHex,
			Data:             append([]byte(nil), c.data...),
			ByteLen:          len(c.data),
			QueryID:          id,
			QueryIDs:         uniqueQueryIDs(c.records),
			Count:            len(c.records),
			CanonicalDataHex: canonical.dataHex,
			CanonicalQueryID: canonicalID,
		})
	}
	return ev, conflicts, canonical
}

func duplicateCount(records []queryRecord) int {
	if len(records) <= 1 {
		return 0
	}
	return len(records) - 1
}

type contiguousRun struct {
	ids      []uint16
	evidence []FragmentEvidence
	data     []byte
	bytes    int
	records  []queryRecord
}

func buildAlert(ws *windowState, cfg Config) *Alert {
	k, b := cfg.thresholds()
	if len(ws.fragments) < k {
		return nil
	}

	indices := make([]uint16, 0, len(ws.fragments))
	for index := range ws.fragments {
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	allEvidence := make([]FragmentEvidence, 0, len(indices))
	allConflicts := make([]Conflict, 0)
	canonical := make(map[uint16]candidateVariant, len(indices))
	for _, index := range indices {
		ev, conflicts, c := buildFragment(ws.fragments[index])
		allEvidence = append(allEvidence, ev)
		allConflicts = append(allConflicts, conflicts...)
		canonical[index] = c
	}
	sort.Slice(allConflicts, func(i, j int) bool {
		if allConflicts[i].Index != allConflicts[j].Index {
			return allConflicts[i].Index < allConflicts[j].Index
		}
		return allConflicts[i].DataHex < allConflicts[j].DataHex
	})

	best := longestRun(indices, allEvidence, canonical)
	if best.bytes < b {
		return nil
	}

	data := make([]byte, 0, best.bytes)
	for _, ev := range best.evidence {
		data = append(data, ev.Data...)
	}
	hexData := hex.EncodeToString(data)
	queryIDs := uniqueQueryIDs(best.records)
	duplicates := 0
	for _, ev := range best.evidence {
		duplicates += ev.Duplicates
	}
	var allRecords []queryRecord
	for _, fs := range ws.fragments {
		for _, v := range fs.variants {
			allRecords = append(allRecords, v.records...)
		}
	}

	w := ws.window
	a := &Alert{
		Client:            ws.key.client,
		ClientID:          ws.key.client,
		RegisteredDomain:  ws.key.registeredDomain,
		Domain:            ws.key.registeredDomain,
		BaseDomain:        ws.key.registeredDomain,
		Window:            w,
		WindowStart:       w.Start,
		WindowEnd:         w.End,
		Start:             w.Start,
		End:               w.End,
		UniqueFragments:   len(ws.fragments),
		DistinctFragments: len(ws.fragments),
		FragmentCount:     len(ws.fragments),
		ChainFragments:    len(best.evidence),
		ReassembledBytes:  len(data),
		ByteCount:         len(data),
		Indices:           append([]uint16(nil), best.ids...),
		Data:              data,
		Reassembled:       append([]byte(nil), data...),
		Payload:           append([]byte(nil), data...),
		DataHex:           hexData,
		Hex:               hexData,
		PayloadHex:        hexData,
		IDs:               append([]string(nil), queryIDs...),
		QueryIDs:          queryIDs,
		OriginalQueryIDs:  append([]string(nil), queryIDs...),
		AllQueryIDs:       uniqueQueryIDs(allRecords),
		Fragments:         append([]FragmentEvidence(nil), best.evidence...),
		Evidence:          append([]FragmentEvidence(nil), best.evidence...),
		AllFragments:      allEvidence,
		Conflicts:         allConflicts,
		Duplicates:        duplicates,
	}
	if len(best.evidence) > 0 {
		a.FirstIndex = best.evidence[0].Index
		a.LastIndex = best.evidence[len(best.evidence)-1].Index
	}
	if len(queryIDs) > 0 {
		a.QueryID = queryIDs[0]
	}
	return a
}

func longestRun(indices []uint16, evidence []FragmentEvidence, canonical map[uint16]candidateVariant) contiguousRun {
	makeRun := func(ids []uint16) contiguousRun {
		run := contiguousRun{ids: append([]uint16(nil), ids...)}
		for _, index := range ids {
			var ev FragmentEvidence
			for i := range evidence {
				if evidence[i].Index == index {
					ev = evidence[i]
					break
				}
			}
			run.evidence = append(run.evidence, ev)
			run.bytes += ev.ByteLen
			run.records = append(run.records, canonical[index].records...)
		}
		return run
	}

	if len(indices) == 0 {
		return contiguousRun{}
	}

	var best contiguousRun
	start := 0
	for i := 1; i <= len(indices); i++ {
		if i < len(indices) && indices[i] == indices[i-1]+1 {
			continue
		}
		run := makeRun(indices[start:i])
		if best.ids == nil || run.bytes > best.bytes ||
			(run.bytes == best.bytes && len(run.evidence) > len(best.evidence)) ||
			(run.bytes == best.bytes && len(run.evidence) == len(best.evidence) && run.ids[0] < best.ids[0]) {
			best = run
		}
		start = i
	}
	return best
}

// Snapshot returns reports for all currently open windows. It does not seal
// them and does not emit alerts.
func (d *Detector) Snapshot() []WindowReport {
	d.ensure()
	reports := make([]WindowReport, 0, len(d.windows))
	for _, ws := range d.windows {
		indices := make([]uint16, 0, len(ws.fragments))
		for index := range ws.fragments {
			indices = append(indices, index)
		}
		sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

		report := WindowReport{
			Client:           ws.key.client,
			RegisteredDomain: ws.key.registeredDomain,
			Window:           ws.window,
			UniqueFragments:  len(ws.fragments),
		}
		for _, index := range indices {
			ev, conflicts, _ := buildFragment(ws.fragments[index])
			report.Fragments = append(report.Fragments, ev)
			report.Conflicts = append(report.Conflicts, conflicts...)
		}
		report.ReassembledBytes = longestContiguousBytes(indices, report.Fragments)
		alert := buildAlert(ws, d.cfg)
		if alert != nil {
			a := *alert
			report.Detected = true
			report.Alert = &a
			report.ReassembledBytes = a.ReassembledBytes
		}
		reports = append(reports, report)
	}
	sort.Slice(reports, func(i, j int) bool {
		if !reports[i].Window.End.Equal(reports[j].Window.End) {
			return reports[i].Window.End.Before(reports[j].Window.End)
		}
		if reports[i].Client != reports[j].Client {
			return reports[i].Client < reports[j].Client
		}
		return reports[i].RegisteredDomain < reports[j].RegisteredDomain
	})
	return reports
}

func longestContiguousBytes(indices []uint16, evidence []FragmentEvidence) int {
	if len(indices) == 0 {
		return 0
	}
	best, current := 0, 0
	for i, index := range indices {
		byteLen := 0
		for j := range evidence {
			if evidence[j].Index == index {
				byteLen = evidence[j].ByteLen
				break
			}
		}
		if i == 0 || index != indices[i-1]+1 {
			current = 0
		}
		current += byteLen
		if current > best {
			best = current
		}
	}
	return best
}

func parseWindowArgs(args []any) (Window, bool) {
	var times []time.Time
	for _, arg := range args {
		switch v := arg.(type) {
		case Window:
			if v.Valid() {
				return v, true
			}
		case *Window:
			if v != nil && v.Valid() {
				return *v, true
			}
		case time.Time:
			times = append(times, v)
		}
	}
	if len(times) >= 2 {
		w := Window{Start: times[0], End: times[1]}
		if w.Valid() {
			return w, true
		}
	}
	return Window{}, false
}

// Evaluate performs a full recomputation for the supplied events. It accepts
// a Window, or (start, end) times, plus an optional Config. When no window is
// supplied, each event's own window is honored and all open windows are closed
// at the end.
func Evaluate(events []Query, args ...any) []Alert {
	return evaluate(Config{}, events, args...)
}

// Detect, Recompute and FullRecompute are aliases for Evaluate.
func Detect(events []Query, args ...any) []Alert        { return Evaluate(events, args...) }
func Recompute(events []Query, args ...any) []Alert     { return Evaluate(events, args...) }
func FullRecompute(events []Query, args ...any) []Alert { return Evaluate(events, args...) }

// Evaluate recomputes using the detector's thresholds unless a Config is
// supplied in args.
func (d *Detector) Evaluate(events []Query, args ...any) []Alert {
	return evaluate(d.Config(), events, args...)
}

func evaluate(base Config, events []Query, args ...any) []Alert {
	cfg := base
	var w Window
	hasWindow := false
	var times []time.Time
	var thresholds []int
	for _, arg := range args {
		switch v := arg.(type) {
		case Config:
			cfg = v
		case *Config:
			if v != nil {
				cfg = *v
			}
		case Window:
			w, hasWindow = v, v.Valid()
		case *Window:
			if v != nil {
				w, hasWindow = *v, v.Valid()
			}
		case time.Time:
			times = append(times, v)
		case int:
			thresholds = append(thresholds, v)
		}
	}
	if len(thresholds) >= 2 {
		cfg.K, cfg.B = thresholds[0], thresholds[1]
	}
	if !hasWindow && len(times) >= 2 {
		w = Window{Start: times[0], End: times[1]}
		hasWindow = w.Valid()
	}

	defaultWindow := cfg.Window
	cfg.Window = nil
	d := newDetector(cfg)
	if hasWindow {
		d.SetWindow(w)
	} else if defaultWindow != nil && defaultWindow.Valid() {
		d.SetWindow(*defaultWindow)
	}
	for _, event := range events {
		if hasWindow {
			event.Window = &w
			event.WindowStart = time.Time{}
			event.WindowEnd = time.Time{}
			event.Start = time.Time{}
			event.End = time.Time{}
		}
		d.Observe(event)
	}
	if hasWindow {
		return d.CloseWindow(w)
	}
	return d.CloseAll()
}

// Reassembled returns a copy of the reconstructed bytes.
func (a Alert) ReassembledData() []byte {
	return append([]byte(nil), a.Data...)
}

// HasConflict reports whether the window contained same-number, different-
// content packets.
func (a Alert) HasConflict() bool {
	return len(a.Conflicts) > 0
}

// EqualData compares reconstructed bytes without depending on slice aliasing.
func (a Alert) EqualData(other Alert) bool {
	return bytes.Equal(a.Data, other.Data)
}
