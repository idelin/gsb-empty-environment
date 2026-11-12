package pubcheck

import (
	"testing"
	"time"
)

const (
	gInit  GoroutineID = "init"
	gA     GoroutineID = "reader-a"
	gB     GoroutineID = "reader-b"
	obj    ObjectID    = "widget"
	secret FieldID     = "secret"
)

func refAt(gen Generation) Ref { return Ref{Object: obj, Generation: gen} }

func chainReason(chain []ChainLink) string {
	for _, link := range chain {
		if !link.HasEdge {
			return link.Description
		}
	}
	return ""
}

// 安全发布：字段写 -> 发布 -> 释放 -> 获取 -> 读取，完整链路无竞争。
func TestSafePublicationWithBarrier(t *testing.T) {
	c := NewChecker()
	c.NewSync("ready", SyncBarrier)
	ref := refAt(1)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "ready")
	c.AcquireSync(gA, "ready")
	c.AcquireRead(gA, ref)

	AssertNoRace(t, c)
}

// 屏障事件交换：一次释放必须被多个读取者的获取事件共同观察到，
// 只有执行了获取的读取者才建立先行发生关系。
func TestBarrierEventExchange(t *testing.T) {
	c := NewChecker()
	c.NewSync("wg", SyncBarrier)
	ref := refAt(1)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "wg")

	c.AcquireSync(gA, "wg")
	c.AcquireRead(gA, ref)

	c.AcquireRead(gB, ref)

	races := AssertRaceCount(t, c, 1)
	if races[0].Read.Goroutine != gB {
		t.Fatalf("expected racer reader-b, got %s", races[0].Read.Goroutine)
	}
	if got := chainReason(races[0].Chain); got != ReasonReleaseNotAcquired {
		t.Fatalf("expected reader-b without matching acquire, got %q", got)
	}

	fixed := NewChecker()
	fixed.NewSync("wg", SyncBarrier)
	fixed.FieldWrite(gInit, ref, secret)
	fixed.Publish(gInit, ref)
	fixed.ReleaseSync(gInit, "wg")
	fixed.AcquireSync(gA, "wg")
	fixed.AcquireRead(gA, ref)
	fixed.AcquireSync(gB, "wg")
	fixed.AcquireRead(gB, ref)
	AssertNoRace(t, fixed)
}

// 屏障支持多次释放与多次获取的全配对交换。
func TestBarrierMultipleReleases(t *testing.T) {
	c := NewChecker()
	c.NewSync("wg", SyncBarrier)
	ref := refAt(7)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "wg")
	c.ReleaseSync(gInit, "wg")
	c.AcquireSync(gA, "wg")
	c.AcquireSync(gB, "wg")
	c.AcquireRead(gA, ref)
	c.AcquireRead(gB, ref)

	AssertNoRace(t, c)
}

// 现实时间先后不是同步：即使读取的 Wall 时间晚于初始化写，
// 缺少释放/获取边时仍必须报告竞争。
func TestWallClockOrderIsNotSynchronization(t *testing.T) {
	c := NewChecker()
	ref := refAt(2)
	base := time.Unix(1_700_000_000, 0)

	c.Emit(Event{Kind: KindFieldWrite, Goroutine: gInit, Ref: ref, Field: secret, Wall: base})
	c.Emit(Event{Kind: KindPublish, Goroutine: gInit, Ref: ref, Wall: base.Add(time.Second)})
	c.Emit(Event{Kind: KindAcquireRead, Goroutine: gA, Ref: ref, Wall: base.Add(time.Hour)})

	g := c.BuildGraph()
	write := g.Events[0]
	read := g.Events[2]
	if g.HappensBefore(write.Seq, read.Seq) {
		t.Fatal("wall-clock order must not establish happens-before")
	}

	races := AssertRaceCount(t, c, 1)
	if got := chainReason(races[0].Chain); got != ReasonPublishNoRelease {
		t.Fatalf("expected missing release, got %q", got)
	}
}

// 即使 Wall 时间与登记顺序“倒挂”，判定也只依赖先行发生图。
func TestWallClockInvertedStillSafeWithSync(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(3)
	base := time.Unix(1_700_000_100, 0)

	c.Emit(Event{Kind: KindFieldWrite, Goroutine: gInit, Ref: ref, Field: secret, Wall: base.Add(time.Hour)})
	c.Emit(Event{Kind: KindPublish, Goroutine: gInit, Ref: ref, Wall: base.Add(2 * time.Hour)})
	c.Emit(Event{Kind: KindReleaseSync, Goroutine: gInit, Sync: "ch", Wall: base.Add(3 * time.Hour)})
	c.Emit(Event{Kind: KindAcquireSync, Goroutine: gA, Sync: "ch", Wall: base})
	c.Emit(Event{Kind: KindAcquireRead, Goroutine: gA, Ref: ref, Wall: base.Add(-time.Hour)})

	AssertNoRace(t, c)
}

// 对象代次重用：旧代次已安全发布不能掩盖新代次未初始化的读取；
// 新代次完成同步后也不能被旧代次的缺失同步污染。
func TestGenerationReuseIsolation(t *testing.T) {
	c := NewChecker()
	c.NewSync("ready", SyncBarrier)
	gen1 := refAt(1)
	gen2 := refAt(2)

	c.FieldWrite(gInit, gen1, secret)
	c.Publish(gInit, gen1)
	c.ReleaseSync(gInit, "ready")
	c.AcquireSync(gA, "ready")
	c.AcquireRead(gA, gen1)

	c.FieldWrite(gInit, gen2, secret)
	c.Publish(gInit, gen2)
	c.AcquireRead(gB, gen2)

	races := AssertRaceCount(t, c, 1)
	if races[0].Ref != gen2 {
		t.Fatalf("expected race on gen2, got %s", races[0].Ref)
	}
	if got := chainReason(races[0].Chain); got != ReasonPublishNoRelease {
		t.Fatalf("expected missing release on gen2, got %q", got)
	}

	fixed := NewChecker()
	fixed.NewSync("ready", SyncBarrier)
	fixed.FieldWrite(gInit, gen1, secret)
	fixed.Publish(gInit, gen1)
	fixed.ReleaseSync(gInit, "ready")
	fixed.AcquireSync(gA, "ready")
	fixed.AcquireRead(gA, gen1)
	fixed.FieldWrite(gInit, gen2, secret)
	fixed.Publish(gInit, gen2)
	fixed.ReleaseSync(gInit, "ready")
	fixed.AcquireSync(gB, "ready")
	fixed.AcquireRead(gB, gen2)
	AssertNoRace(t, fixed)
}

// 不同代次上的同名字段读写互不构成竞争。
func TestDifferentGenerationsNeverConflict(t *testing.T) {
	c := NewChecker()
	c.FieldWrite(gInit, refAt(1), secret)
	c.FieldWrite(gInit, refAt(2), secret)
	c.AcquireRead(gA, refAt(3))

	AssertNoRace(t, c)
}

// 先发布指针、后写字段：第一跳程序顺序边缺失，根因明确。
func TestPublishBeforeFieldWrite(t *testing.T) {
	c := NewChecker()
	ref := refAt(4)

	c.Publish(gInit, ref)
	c.FieldWrite(gInit, ref, secret)
	c.ReleaseSync(gInit, "ready")
	c.AcquireSync(gA, "ready")
	c.AcquireRead(gA, ref)

	races := AssertRaceCount(t, c, 1)
	if got := chainReason(races[0].Chain); got != ReasonPublishBeforeWrite {
		t.Fatalf("expected publish-before-write, got %q", got)
	}
}

// 发布后没有释放事件：指针写只是普通内存写，不产生同步。
func TestPublishWithoutRelease(t *testing.T) {
	c := NewChecker()
	ref := refAt(5)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.AcquireRead(gA, ref)

	races := AssertRaceCount(t, c, 1)
	if got := chainReason(races[0].Chain); got != ReasonPublishNoRelease {
		t.Fatalf("expected missing release, got %q", got)
	}
}

// 通道模式严格 1:1 FIFO 配对：一次发送只能唤醒一次接收，
// 第二个接收者拿不到同步边，即使它读取的时间更晚。
func TestChannelFIFOPairing(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(6)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "ch")

	c.AcquireSync(gA, "ch")
	c.AcquireRead(gA, ref)

	c.AcquireSync(gB, "ch")
	c.AcquireRead(gB, ref)

	races := AssertRaceCount(t, c, 1)
	if races[0].Read.Goroutine != gB {
		t.Fatalf("expected reader-b to race, got %s", races[0].Read.Goroutine)
	}
	if got := chainReason(races[0].Chain); got != ReasonReleaseNotAcquired {
		t.Fatalf("expected reader-b without matching acquire, got %q", got)
	}

	fixed := NewChecker()
	fixed.NewSync("ch", SyncChannel)
	fixed.FieldWrite(gInit, ref, secret)
	fixed.Publish(gInit, ref)
	fixed.ReleaseSync(gInit, "ch")
	fixed.AcquireSync(gA, "ch")
	fixed.AcquireRead(gA, ref)
	fixed.ReleaseSync(gInit, "ch")
	fixed.AcquireSync(gB, "ch")
	fixed.AcquireRead(gB, ref)
	AssertNoRace(t, fixed)
}

// 获取之后才解引用才安全；先读取后获取不构成先行发生。
func TestAcquireAfterReadIsUnsafe(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(8)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "ch")
	c.AcquireRead(gA, ref)
	c.AcquireSync(gA, "ch")

	races := AssertRaceCount(t, c, 1)
	if got := chainReason(races[0].Chain); got != ReasonAcquireBeforeRead {
		t.Fatalf("reader acquires after reading, expected acquire-before-read violation, got %q", got)
	}
}

// 同步原语已完成交换，但读取 goroutine 在自身获取事件之前就解引用指针：
// 最后一跳程序顺序缺失，根因定位到 acquire -> read。
func TestAcquireEdgeMissingInReaderGoroutine(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(81)

	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "ch")
	c.AcquireSync(gB, "ch")
	c.AcquireRead(gA, ref)

	races := AssertRaceCount(t, c, 1)
	if got := chainReason(races[0].Chain); got != ReasonReleaseNotAcquired {
		t.Fatalf("expected reader-a to lack a matching acquire, got %q", got)
	}
}

// 同一 goroutine 内的写读由程序顺序保证，不算跨 goroutine 发布竞争。
func TestSameGoroutineProgramOrder(t *testing.T) {
	c := NewChecker()
	ref := refAt(9)
	c.FieldWrite(gInit, ref, secret)
	c.Publish(gInit, ref)
	c.AcquireRead(gInit, ref)

	AssertNoRace(t, c)
}

// 报告必须包含具体竞争对、字段与完整诊断路径。
func TestRaceReportContent(t *testing.T) {
	c := NewChecker()
	ref := refAt(10)
	write := c.FieldWrite(gInit, ref, secret)
	read := c.AcquireRead(gA, ref)

	races := c.Check()
	if len(races) != 1 {
		t.Fatalf("expected 1 race, got %d", len(races))
	}
	race := races[0]
	if race.FieldWrite.Seq != write.Seq || race.Read.Seq != read.Seq {
		t.Fatalf("race pair mismatch: %+v", race)
	}
	if race.Field != secret || race.Ref != ref {
		t.Fatalf("race target mismatch: %+v", race)
	}
	if len(race.Chain) == 0 || race.Chain[0].Stage != StageFieldWriteToPublish {
		t.Fatalf("chain must start at field-write stage: %+v", race.Chain)
	}
	if chainReason(race.Chain) != ReasonNoPublish {
		t.Fatalf("expected no-publish reason, got %q", chainReason(race.Chain))
	}
	if msg := race.Message(); msg == "" || race.Reason != ReasonUnsafeRead {
		t.Fatalf("unexpected message/reason: %q %q", msg, race.Reason)
	}
	rendered := FormatChain(race.Chain)
	if rendered == "" || !contains(rendered, "MISS") {
		t.Fatalf("rendered chain must flag a missing edge: %q", rendered)
	}
}

// 路径查询仅基于图边，程序顺序与同步边串联可达。
func TestGraphPathAndEdges(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(11)
	w := c.FieldWrite(gInit, ref, secret)
	p := c.Publish(gInit, ref)
	rel := c.ReleaseSync(gInit, "ch")
	acq := c.AcquireSync(gA, "ch")
	r := c.AcquireRead(gA, ref)

	g := c.BuildGraph()
	if !g.HappensBefore(w.Seq, r.Seq) {
		t.Fatal("write should happen-before read via full publication chain")
	}
	path := g.Path(w.Seq, r.Seq)
	want := []int{w.Seq, p.Seq, rel.Seq, acq.Seq, r.Seq}
	if len(path) != len(want) {
		t.Fatalf("path length mismatch: got %v want %v", path, want)
	}
	for i, seq := range path {
		if seq != want[i] {
			t.Fatalf("path mismatch: got %v want %v", path, want)
		}
	}

	var sawSW bool
	for _, e := range g.Edges {
		if e.Kind == EdgeSynchronizesWith && e.From == rel.Seq && e.To == acq.Seq {
			sawSW = true
		}
	}
	if !sawSW {
		t.Fatal("missing synchronizes-with edge between release and acquire")
	}
}

// 多字段对象：任一敏感字段缺少先行发生关系都要独立报告。
func TestMultipleFieldsReportedSeparately(t *testing.T) {
	c := NewChecker()
	c.NewSync("ch", SyncChannel)
	ref := refAt(12)

	c.FieldWrite(gInit, ref, "a")
	c.FieldWrite(gInit, ref, "b")
	c.Publish(gInit, ref)
	c.ReleaseSync(gInit, "ch")
	c.AcquireSync(gA, "ch")
	c.AcquireRead(gA, ref)

	AssertNoRace(t, c)

	c2 := NewChecker()
	c2.FieldWrite(gInit, ref, "a")
	c2.FieldWrite(gInit, ref, "b")
	c2.Publish(gInit, ref)
	c2.AcquireRead(gA, ref)
	races := c2.Check()
	if len(races) != 2 {
		t.Fatalf("expected one race per field, got %d", len(races))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
