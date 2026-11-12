package pubcheck

import "strings"

// 诊断原因常量，取值稳定，宿主测试可直接按字符串断言。
const (
	ReasonUnsafeRead         = "read lacks happens-before edge from field write"
	ReasonNoPublish          = "no pointer publish reaches the reader's object generation"
	ReasonPublishBeforeWrite = "pointer is published before the sensitive field write"
	ReasonPublishNoRelease   = "publish is not followed by a release synchronization"
	ReasonReleaseNotAcquired = "release synchronization has no matching acquire"
	ReasonAcquireBeforeRead  = "reader does not acquire before dereferencing the pointer"
)

// 诊断链阶段名称，取值稳定，用于测试断言与报告渲染。
const (
	StageFieldWriteToPublish = "field-write -> publish"
	StagePublishToRelease    = "publish -> release-sync"
	StageReleaseToAcquire    = "release-sync -> acquire-sync"
	StageAcquireToRead       = "acquire-sync -> acquire-read"
)

// ChainLink 是安全发布链路上的一跳诊断信息。
type ChainLink struct {
	Stage       string
	From        Event
	To          Event
	HasEdge     bool
	Description string
}

// Race 描述一次缺少初始化先行关系的读取竞争。
type Race struct {
	FieldWrite Event
	Read       Event
	Ref        Ref
	Field      FieldID
	Reason     string
	Chain      []ChainLink
}

// Message 返回单行可读报告。
func (r Race) Message() string {
	return r.Reason + ": " + r.FieldWrite.Label() + " vs " + r.Read.Label()
}

type fieldKey struct {
	ref   Ref
	field FieldID
}

// Check 构造先行发生图并返回所有未与字段初始化建立先行发生关系的读取竞争。
// 检查严格限定在同一对象代次内：不同代次的读写互不构成竞争，也不会互相掩盖。
func (c *Checker) Check() []Race {
	g := c.BuildGraph()

	var reads []Event
	writes := make(map[fieldKey][]Event)
	publishes := make(map[Ref][]Event)
	for _, ev := range g.Events {
		switch ev.Kind {
		case KindAcquireRead:
			reads = append(reads, ev)
		case KindFieldWrite:
			key := fieldKey{ref: ev.Ref, field: ev.Field}
			writes[key] = append(writes[key], ev)
		case KindPublish:
			publishes[ev.Ref] = append(publishes[ev.Ref], ev)
		}
	}

	var races []Race
	for _, read := range reads {
		fieldSet := fieldsForRead(writes, read.Ref)
		for field, fieldWrites := range fieldSet {
			for _, write := range fieldWrites {
				if write.Goroutine == read.Goroutine {
					continue
				}
				if safeRead(g, c.syncs, write, read, publishes) {
					continue
				}
				races = append(races, Race{
					FieldWrite: write,
					Read:       read,
					Ref:        read.Ref,
					Field:      field,
					Reason:     ReasonUnsafeRead,
					Chain:      buildChain(g, c.syncs, write, read, publishes),
				})
			}
		}
	}
	return races
}

// safeRead 判定读取是否通过合法安全发布链观察到字段写：
// 字段写必须在发布之前由程序顺序保证，再经释放/获取同步传到读取 goroutine。
// 同一 goroutine 内从写绕行到发布的程序顺序（先发布后写场景）不被承认。
func safeRead(g *Graph, modes map[SyncID]SyncMode, write, read Event, publishes map[Ref][]Event) bool {
	publish := firstPublish(g, publishes[write.Ref], write.Goroutine)
	if publish == nil || !sameGoroutineBefore(g, write.Seq, publish.Seq) {
		return false
	}
	release := firstEvent(g, len(g.Events), KindReleaseSync, func(ev Event) bool {
		return ev.Goroutine == publish.Goroutine && g.HappensBefore(publish.Seq, ev.Seq)
	})
	if release == nil {
		return false
	}
	return releaseChainWorks(g, modes, read, *publish, *release)
}

// releaseChainWorks 报告从某个释放事件出发，读取 goroutine 是否存在
// “释放 -> 获取（真实同步边）-> 读取（程序顺序）”的完整链路。
func releaseChainWorks(g *Graph, modes map[SyncID]SyncMode, read, publish, release Event) bool {
	for _, ev := range g.Events {
		if ev.Seq < release.Seq || ev.Kind != KindReleaseSync || ev.Goroutine != publish.Goroutine {
			continue
		}
		if !g.HappensBefore(publish.Seq, ev.Seq) {
			continue
		}
		acquire := pairedAcquire(g, modes, read, ev)
		if acquire != nil && edgeExists(g, ev.Seq, acquire.Seq) &&
			directProgramOrder(g, acquire.Seq, read.Seq) {
			return true
		}
	}
	return false
}

func fieldsForRead(writes map[fieldKey][]Event, ref Ref) map[FieldID][]Event {
	out := make(map[FieldID][]Event)
	for key, evs := range writes {
		if key.ref == ref {
			out[key.field] = evs
		}
	}
	return out
}

// buildChain 沿“字段写 -> 发布 -> 释放 -> 获取 -> 读取”的安全发布链逐跳验证，
// 第一跳缺失边即为竞争根因，后续阶段不再虚构。
func buildChain(g *Graph, modes map[SyncID]SyncMode, write, read Event, publishes map[Ref][]Event) []ChainLink {
	var links []ChainLink

	publish := firstPublish(g, publishes[write.Ref], write.Goroutine)
	if publish == nil {
		links = append(links, ChainLink{
			Stage:       StageFieldWriteToPublish,
			From:        write,
			To:          read,
			HasEdge:     false,
			Description: ReasonNoPublish,
		})
		return links
	}

	links = append(links, ChainLink{
		Stage:       StageFieldWriteToPublish,
		From:        write,
		To:          *publish,
		HasEdge:     sameGoroutineBefore(g, write.Seq, publish.Seq),
		Description: "field write must be visible before the pointer leaves the goroutine",
	})
	if !sameGoroutineBefore(g, write.Seq, publish.Seq) {
		links[0].Description = ReasonPublishBeforeWrite
	}

	release := firstEvent(g, read.Seq, KindReleaseSync, func(ev Event) bool {
		return ev.Goroutine == publish.Goroutine && g.HappensBefore(publish.Seq, ev.Seq)
	})
	if release == nil {
		if links[0].HasEdge {
			links = append(links, ChainLink{
				Stage:       StagePublishToRelease,
				From:        *publish,
				To:          read,
				HasEdge:     false,
				Description: ReasonPublishNoRelease,
			})
			return links
		}
		return links
	}
	acquireProbe := pairedAcquire(g, modes, read, *release)
	if acquireProbe == nil || !edgeExists(g, release.Seq, acquireProbe.Seq) {
		if alt := nextViableRelease(g, modes, read, *publish, release.Seq-1); alt != nil {
			release = alt
		}
	}
	links = append(links, ChainLink{
		Stage:       StagePublishToRelease,
		From:        *publish,
		To:          *release,
		HasEdge:     g.HappensBefore(publish.Seq, release.Seq),
		Description: "publisher must execute a release after exposing the pointer",
	})

	acquire := pairedAcquire(g, modes, read, *release)
	if acquire == nil {
		if links[0].HasEdge {
			links = append(links, ChainLink{
				Stage:       StageReleaseToAcquire,
				From:        *release,
				To:          read,
				HasEdge:     false,
				Description: ReasonReleaseNotAcquired,
			})
			return links
		}
		return links
	}
	links = append(links, ChainLink{
		Stage:       StageReleaseToAcquire,
		From:        *release,
		To:          *acquire,
		HasEdge:     edgeExists(g, release.Seq, acquire.Seq),
		Description: "release must synchronize-with an acquire on the same primitive",
	})
	if !links[len(links)-1].HasEdge {
		links[len(links)-1].Description = ReasonReleaseNotAcquired
	}

	links = append(links, ChainLink{
		Stage:       StageAcquireToRead,
		From:        *acquire,
		To:          read,
		HasEdge:     g.HappensBefore(acquire.Seq, read.Seq),
		Description: ReasonAcquireBeforeRead,
	})
	return links
}

// directProgramOrder 只承认同一 goroutine 内的程序顺序边，
// 不把经由同步事件绕行得到的可达性当作本跳的程序顺序。
func directProgramOrder(g *Graph, from, to int) bool {
	for _, e := range g.adj[from] {
		if e.Kind == EdgeProgramOrder && e.To == to {
			return true
		}
	}
	return false
}

// sameGoroutineBefore 报告两个事件是否在同一 goroutine 内由程序顺序先行发生，
// 允许中间隔着同 goroutine 的其他事件（例如多个字段写后统一发布）。
func sameGoroutineBefore(g *Graph, from, to int) bool {
	if from >= len(g.Events) || to >= len(g.Events) {
		return false
	}
	if g.Events[from].Goroutine != g.Events[to].Goroutine {
		return false
	}
	return from < to
}

// firstPublish 返回发布 goroutine 上该对象代次的第一次指针发布。
// 发布与字段写的先后关系不在此假设，交由 directProgramOrder 判定，
// 这样“先发布指针、后初始化字段”仍能定位到正确的发布事件。
func firstPublish(g *Graph, candidates []Event, writer GoroutineID) *Event {
	for i := range candidates {
		if candidates[i].Goroutine == writer {
			return &candidates[i]
		}
	}
	return nil
}

func firstEvent(g *Graph, beforeSeq int, kind Kind, match func(Event) bool) *Event {
	for _, ev := range g.Events {
		if ev.Seq >= beforeSeq {
			break
		}
		if ev.Kind == kind && match(ev) {
			return &ev
		}
	}
	return nil
}

// nextViableRelease 在当前释放事件之后寻找能与读取者建立真实同步边的释放事件，
// 用于通道 FIFO 模式下前一次发送已被其他 goroutine 消费的场景。
func nextViableRelease(g *Graph, modes map[SyncID]SyncMode, read, publish Event, afterSeq int) *Event {
	for _, ev := range g.Events {
		if ev.Seq <= afterSeq || ev.Kind != KindReleaseSync {
			continue
		}
		if ev.Goroutine != publish.Goroutine || !g.HappensBefore(publish.Seq, ev.Seq) {
			continue
		}
		acquire := pairedAcquire(g, modes, read, ev)
		if acquire != nil && edgeExists(g, ev.Seq, acquire.Seq) &&
			directProgramOrder(g, acquire.Seq, read.Seq) {
			return &ev
		}
	}
	return nil
}

func pairedAcquire(g *Graph, modes map[SyncID]SyncMode, read Event, release Event) *Event {
	var fallback *Event
	for _, ev := range g.Events {
		if ev.Kind != KindAcquireSync || ev.Sync != release.Sync {
			continue
		}
		if ev.Goroutine != read.Goroutine {
			continue
		}
		if modes[release.Sync] == SyncBarrier {
			if ev.Seq >= read.Seq {
				continue
			}
			if g.HappensBefore(release.Seq, ev.Seq) && directProgramOrder(g, ev.Seq, read.Seq) {
				return &ev
			}
			continue
		}
		// 通道模式：存在真实同步边且读取在获取之后才是合法配对。
		if ev.Seq < read.Seq && directProgramOrder(g, ev.Seq, read.Seq) {
			if edgeExists(g, release.Seq, ev.Seq) {
				return &ev
			}
			if fallback == nil {
				fallback = &ev
			}
		}
		// 读取发生在获取之前：获取无法保护该读取，仍记录为回退用于诊断最后一跳。
		if ev.Seq >= read.Seq && fallback == nil {
			fallback = &ev
		}
	}
	return fallback
}

func edgeExists(g *Graph, from, to int) bool {
	for _, e := range g.Edges {
		if e.Kind == EdgeSynchronizesWith && e.From == from && e.To == to {
			return true
		}
	}
	return false
}

// FormatChain 将诊断链渲染为多行文本，供测试宿主打印失败详情。
func FormatChain(chain []ChainLink) string {
	var b strings.Builder
	for _, link := range chain {
		status := "OK  "
		if !link.HasEdge {
			status = "MISS"
		}
		b.WriteString("  [")
		b.WriteString(status)
		b.WriteString("] ")
		b.WriteString(link.Stage)
		b.WriteString(": ")
		b.WriteString(link.From.Label())
		b.WriteString(" -> ")
		b.WriteString(link.To.Label())
		b.WriteString("  // ")
		b.WriteString(link.Description)
		b.WriteByte('\n')
	}
	return b.String()
}
