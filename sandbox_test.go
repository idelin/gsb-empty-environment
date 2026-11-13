package netsec

import (
	"errors"
	"testing"
)

// fixture 聚合一次试验所需的共享对象。
type fixture struct {
	sb       *Sandbox
	base     *Table // 基础环境（共享）
	string   *Table // 只读标准库 string
	format   *Func  // 内置 string.format
	strMeta  *Table // 只读共享字符串元表
	mutable  *Table // 未冻结的可变普通表
	closureA *Func  // 线程 A 的脚本闭包
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	base := NewTable("base_global").AsShared()

	format := &Func{Name: "string.format", Builtin: true}
	stringLib := NewTable("string").AsReadOnly()
	stringLib.Fields[StringValue("format")] = FuncValue(format)
	base.Fields[StringValue("string")] = TableValue(stringLib)

	strMeta := NewTable("string_metatable").AsReadOnly()

	mutable := NewTable("mutable")

	closureA := &Func{Name: "script_a"}

	sb, err := New(Config{Threads: []ThreadConfig{
		{Name: "A", Target: base},
		{Name: "B", Target: base},
	}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	closureA.Env = sb.Thread("A")

	return &fixture{
		sb:       sb,
		base:     base,
		string:   stringLib,
		format:   format,
		strMeta:  strMeta,
		mutable:  mutable,
		closureA: closureA,
	}
}

func globalPath(keys ...string) *Path {
	p := &Path{}
	for _, k := range keys {
		p.Keys = append(p.Keys, StringValue(k))
	}
	return p
}

func eventTypes(evs []Event) []EventType {
	out := make([]EventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}

func containsType(types []EventType, want EventType) bool {
	for _, got := range types {
		if got == want {
			return true
		}
	}
	return false
}

// 场景一：改写 string.format。普通赋值穿透代理命中只读标准库，必须拒绝，
// 内置函数原引用保持不变，且该共享表不出现在变更报告里。
func TestRewriteLibraryFunctionBlocked(t *testing.T) {
	f := newFixture(t)
	evil := &Func{Name: "evil_format"}

	report := f.sb.Execute([]Op{{
		Type:   OpAssign,
		Thread: "A",
		Target: Target{Path: globalPath("string", "format")},
		Key:    StringValue("format"),
		Value:  FuncValue(evil),
	}})

	if err := report.Results[0].Err; !errors.Is(err, ErrReadOnly) {
		t.Fatalf("assign string.format: want ErrReadOnly, got %v", err)
	}
	if got := f.string.Fields[StringValue("format")]; got.Func != f.format {
		t.Fatalf("builtin string.format was altered: got %v", got)
	}
	if len(report.ChangedTables()) != 0 {
		t.Fatalf("no shared table should change, got %v", report.ChangedTables())
	}
	if !containsType(eventTypes(report.Events), EventBlockedReadonly) {
		t.Fatalf("missing blocked_readonly event: %+v", report.Events)
	}
}

// 场景二：rawset 绕过。rawset 不走元方法，但权限闸门不能被绕过：
// 对只读标准库直接 rawset、对共享基础环境 rawset 都必须失败。
func TestRawSetCannotBypass(t *testing.T) {
	f := newFixture(t)

	report := f.sb.Execute([]Op{
		{
			Type:   OpRawSet,
			Thread: "A",
			Target: Target{Table: f.string},
			Key:    StringValue("format"),
			Value:  StringValue("hijacked"),
		},
		{
			Type:   OpRawSet,
			Thread: "A",
			Target: Target{Table: f.base},
			Key:    StringValue("backdoor"),
			Value:  BoolValue(true),
		},
		// 代理上的 rawset 仍然只落线程私有表，是合法路径。
		{
			Type:   OpRawSet,
			Thread: "A",
			Target: Target{Path: globalPath()},
			Key:    StringValue("private_raw"),
			Value:  BoolValue(true),
		},
	})

	if err := report.Results[0].Err; !errors.Is(err, ErrReadOnly) {
		t.Fatalf("rawset readonly lib: want ErrReadOnly, got %v", err)
	}
	if err := report.Results[1].Err; !errors.Is(err, ErrSharedWrite) {
		t.Fatalf("rawset shared base env: want ErrSharedWrite, got %v", err)
	}
	if err := report.Results[2].Err; err != nil {
		t.Fatalf("rawset on private proxy should succeed: %v", err)
	}
	if _, ok := f.base.Fields[StringValue("backdoor")]; ok {
		t.Fatal("shared base environment received a rawset write")
	}
	if got := f.string.Fields[StringValue("format")]; got.Func != f.format {
		t.Fatalf("string.format overwritten via rawset: got %v", got)
	}
	if v := f.sb.Thread("A").Fields[StringValue("private_raw")]; !v.Bool {
		t.Fatal("proxy rawset did not land in thread-private storage")
	}
}

// 场景三：给共享字符串元表追加字段或替换元表。
// 只读标记同时挡住 rawset 与 setmetatable，元表对象保持原样。
func TestStringMetatableLocked(t *testing.T) {
	f := newFixture(t)
	customMeta := NewTable("custom_meta")

	report := f.sb.Execute([]Op{
		{
			Type:   OpRawSet,
			Thread: "A",
			Target: Target{Table: f.strMeta},
			Key:    StringValue("__index"),
			Value:  TableValue(customMeta),
		},
		{
			Type:   OpSetMeta,
			Thread: "A",
			Target: Target{Table: f.strMeta},
			Meta:   customMeta,
		},
	})

	for i, res := range report.Results {
		if !errors.Is(res.Err, ErrReadOnly) {
			t.Fatalf("op %d on string metatable: want ErrReadOnly, got %v", i, res.Err)
		}
	}
	if _, ok := f.strMeta.Fields[StringValue("__index")]; ok {
		t.Fatal("read-only string metatable gained a field")
	}
	if f.strMeta.Metatable != nil {
		t.Fatalf("read-only string metatable metatable changed: %v", f.strMeta.Metatable)
	}
}

// 场景四：线程私有赋值。代理写入只落私有表，不触碰基础环境；
// 另一线程既看不到该私有键，自己的同名写入也互不干扰。
func TestThreadPrivateAssignment(t *testing.T) {
	f := newFixture(t)

	report := f.sb.Execute([]Op{
		{
			Type:   OpAssign,
			Thread: "A",
			Target: Target{Path: globalPath()},
			Key:    StringValue("token"),
			Value:  StringValue("A-secret"),
		},
		{
			Type:   OpAssign,
			Thread: "B",
			Target: Target{Path: globalPath()},
			Key:    StringValue("token"),
			Value:  StringValue("B-secret"),
		},
	})

	for i, res := range report.Results {
		if res.Err != nil {
			t.Fatalf("private assign op %d: %v", i, res.Err)
		}
	}
	if _, ok := f.base.Fields[StringValue("token")]; ok {
		t.Fatal("private global write leaked into shared base environment")
	}
	if got := f.sb.Thread("A").Fields[StringValue("token")].Str; got != "A-secret" {
		t.Fatalf("thread A private value = %q", got)
	}
	if got := f.sb.Thread("B").Fields[StringValue("token")].Str; got != "B-secret" {
		t.Fatalf("thread B private value = %q", got)
	}
	// 代理读取仍可回退到基础环境里的共享标准库。
	got, err := f.sb.Get("A", *globalPath("string", "format"))
	if err != nil || got.Func != f.format {
		t.Fatalf("proxy fallback read failed: value=%v err=%v", got, err)
	}
	if len(report.ChangedTables()) != 0 {
		t.Fatalf("private writes must not appear as shared changes: %v", report.ChangedTables())
	}
	if !containsType(eventTypes(report.Events), EventPrivateWrite) {
		t.Fatalf("missing private_write event: %+v", report.Events)
	}
}

// 场景五：table.freeze 之后任何赋值、rawset、setmetatable 都被拒绝，
// 且对只读表再次 freeze 本身也是错误。
func TestFrozenTableRejectsWrites(t *testing.T) {
	f := newFixture(t)

	report := f.sb.Execute([]Op{
		{Type: OpTableFreeze, Thread: "A", Target: Target{Table: f.mutable}},
		{
			Type:   OpAssign,
			Thread: "A",
			Target: Target{Table: f.mutable},
			Key:    StringValue("k"),
			Value:  NumberValue(1),
		},
		{
			Type:   OpRawSet,
			Thread: "A",
			Target: Target{Table: f.mutable},
			Key:    StringValue("k"),
			Value:  NumberValue(2),
		},
		{
			Type:   OpSetMeta,
			Thread: "A",
			Target: Target{Table: f.mutable},
			Meta:   NewTable("m"),
		},
		// 对构造期只读表重复冻结。
		{Type: OpTableFreeze, Thread: "A", Target: Target{Table: f.string}},
	})

	if report.Results[0].Err != nil {
		t.Fatalf("freeze mutable table: %v", report.Results[0].Err)
	}
	for _, idx := range []int{1, 2, 3} {
		if !errors.Is(report.Results[idx].Err, ErrFrozen) {
			t.Fatalf("post-freeze op %d: want ErrFrozen, got %v", idx, report.Results[idx].Err)
		}
	}
	if !errors.Is(report.Results[4].Err, ErrReadOnly) {
		t.Fatalf("freeze readonly table: want ErrReadOnly, got %v", report.Results[4].Err)
	}
	if _, ok := f.mutable.Fields[StringValue("k")]; ok {
		t.Fatal("frozen table accepted a write")
	}
	if f.mutable.Metatable != nil {
		t.Fatal("frozen table accepted a metatable change")
	}
	if !containsType(eventTypes(report.Events), EventBlockedFrozen) {
		t.Fatalf("missing blocked_frozen event: %+v", report.Events)
	}
}

// getfenv 不得把基础环境从内置函数身上泄露出去；脚本闭包只拿到自己的
// 线程代理，setfenv 改写内置函数同样被拒绝。
func TestFunctionEnvironmentProtection(t *testing.T) {
	f := newFixture(t)

	report := f.sb.Execute([]Op{
		{Type: OpGetFenv, Thread: "A", Func: f.format},
		{Type: OpGetFenv, Thread: "A"}, // 不带函数：当前线程环境
		{Type: OpGetFenv, Thread: "A", Func: f.closureA},
		{Type: OpSetFenv, Thread: "A", Func: f.format, Env: f.sb.Thread("A")},
	})

	if !errors.Is(report.Results[0].Err, ErrBuiltinEnv) {
		t.Fatalf("getfenv(builtin): want ErrBuiltinEnv, got %v", report.Results[0].Err)
	}
	if got := report.Results[1].Value.Table; got != f.sb.Thread("A") {
		t.Fatalf("getfenv() must return thread proxy, got %v", got)
	}
	if got := report.Results[2].Value.Table; got != f.sb.Thread("A") {
		t.Fatalf("getfenv(closure): want thread proxy, got %v", got)
	}
	if !errors.Is(report.Results[3].Err, ErrBuiltinEnv) {
		t.Fatalf("setfenv(builtin): want ErrBuiltinEnv, got %v", report.Results[3].Err)
	}
}

// 变更报告：被拦截的攻击不产生共享表改动；合法的共享表 freeze 会被记录，
// 并能通过表名在报告中定位。
func TestSharedChangeReport(t *testing.T) {
	f := newFixture(t)

	attacks := f.sb.Execute([]Op{
		{
			Type:   OpAssign,
			Thread: "A",
			Target: Target{Path: globalPath("string", "format")},
			Key:    StringValue("format"),
			Value:  StringValue("x"),
		},
		{
			Type:   OpSetMeta,
			Thread: "A",
			Target: Target{Table: f.strMeta},
			Meta:   NewTable("m"),
		},
	})
	if len(attacks.ChangedTables()) != 0 {
		t.Fatalf("blocked attacks must not report changes: %v", attacks.ChangedTables())
	}

	shared := NewTable("shared_config").AsShared()
	report := f.sb.Execute([]Op{
		{Type: OpTableFreeze, Thread: "A", Target: Target{Table: shared}},
	})
	changes := report.Changes[shared]
	if changes == nil || changes.Freezes != 1 {
		t.Fatalf("want freeze recorded for shared table, got %+v", changes)
	}
	if found := report.ChangedTables(); len(found) != 1 || found[0] != shared {
		t.Fatalf("ChangedTables = %v, want only %v", found, shared)
	}
}

// 代理影子写入：线程 A 对共享键 string 做私有遮蔽后，只影响自己的视图，
// 基础环境与线程 B 的解析结果保持不变。
func TestProxyShadowingIsLocal(t *testing.T) {
	f := newFixture(t)
	fakeString := NewTable("fake_string")

	report := f.sb.Execute([]Op{{
		Type:   OpAssign,
		Thread: "A",
		Target: Target{Path: globalPath()},
		Key:    StringValue("string"),
		Value:  TableValue(fakeString),
	}})
	if report.Results[0].Err != nil {
		t.Fatalf("shadow assign: %v", report.Results[0].Err)
	}

	gotA, err := f.sb.Get("A", *globalPath("string"))
	if err != nil || gotA.Table != fakeString {
		t.Fatalf("thread A should see its shadow, got %v err=%v", gotA, err)
	}
	gotB, err := f.sb.Get("B", *globalPath("string"))
	if err != nil || gotB.Table != f.string {
		t.Fatalf("thread B should still see shared string lib, got %v err=%v", gotB, err)
	}
	if f.base.Fields[StringValue("string")].Table != f.string {
		t.Fatal("shadow write modified the shared base environment")
	}
}

// 边界行为：私有代理上的元表与闭包环境改写允许；各种非法目标与未知操作
// 返回 ErrInvalidTarget，辅助函数行为稳定。
func TestEdgeCases(t *testing.T) {
	f := newFixture(t)
	proxyMeta := NewTable("proxy_meta")

	report := f.sb.Execute([]Op{
		// 线程代理是私有的，setmetatable 合法。
		{Type: OpSetMeta, Thread: "A", Target: Target{Path: globalPath()}, Meta: proxyMeta},
		// 脚本闭包的 setfenv 合法，且可换入私有表。
		{Type: OpSetFenv, Thread: "A", Func: f.closureA, Env: f.sb.Thread("B")},
		// 未知线程。
		{Type: OpAssign, Thread: "Z", Target: Target{Path: globalPath()}, Key: StringValue("x")},
		// 路径中途不是表。
		{
			Type:   OpAssign,
			Thread: "A",
			Target: Target{Path: globalPath("string", "format", "x")},
			Key:    StringValue("x"),
		},
		// 未知操作类型。
		{Type: "bogus", Thread: "A"},
		// setfenv 未给函数。
		{Type: OpSetFenv, Thread: "A", Env: f.sb.Thread("A")},
	})

	if report.Results[0].Err != nil {
		t.Fatalf("private proxy setmetatable: %v", report.Results[0].Err)
	}
	if f.sb.Thread("A").Metatable != proxyMeta {
		t.Fatal("proxy metatable was not stored privately")
	}
	if report.Results[1].Err != nil || f.closureA.Env != f.sb.Thread("B") {
		t.Fatalf("closure setfenv failed: err=%v env=%v", report.Results[1].Err, f.closureA.Env)
	}
	for _, idx := range []int{2, 3, 4, 5} {
		if !errors.Is(report.Results[idx].Err, ErrInvalidTarget) {
			t.Fatalf("op %d: want ErrInvalidTarget, got %v", idx, report.Results[idx].Err)
		}
	}

	// 无环境闭包的 getfenv 返回 nil 而非报错。
	noEnv := &Func{Name: "no_env"}
	got := f.sb.Execute([]Op{{Type: OpGetFenv, Thread: "A", Func: noEnv}}).Results[0].Value
	if !got.IsNil() {
		t.Fatalf("getfenv on env-less closure = %v, want nil", got)
	}

	// 辅助函数。
	if !NilValue().IsNil() {
		t.Fatal("NilValue must report nil")
	}
	if RawGet(f.base, StringValue("string")).Table != f.string {
		t.Fatal("RawGet did not return the raw field")
	}
	if !RawGet(nil, StringValue("x")).IsNil() {
		t.Fatal("RawGet(nil) must be nil")
	}
	if f.base.String() != "table(base_global)" {
		t.Fatalf("unexpected Table.String: %q", f.base.String())
	}
	if (*Table)(nil).String() != "<nil table>" {
		t.Fatal("nil table String mismatch")
	}

	// Get 的错误与未命中路径。
	if _, err := f.sb.Get("Z", *globalPath("string")); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("Get unknown thread: %v", err)
	}
	if v, err := f.sb.Get("A", *globalPath("missing")); err != nil || !v.IsNil() {
		t.Fatalf("Get missing key: value=%v err=%v", v, err)
	}
	// Get 路径中途解析到非表值。
	if _, err := f.sb.Get("A", *globalPath("string", "format", "x")); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("Get through non-table: %v", err)
	}
	// 写路径键不存在。
	bad := f.sb.Execute([]Op{{
		Type:   OpRawSet,
		Thread: "A",
		Target: Target{Path: globalPath("nope", "k")},
		Key:    StringValue("k"),
	}})
	if !errors.Is(bad.Results[0].Err, ErrInvalidTarget) {
		t.Fatalf("write via missing path: %v", bad.Results[0].Err)
	}
}

// New 的构造期校验。
func TestNewValidation(t *testing.T) {
	if _, err := New(Config{Threads: []ThreadConfig{{Name: ""}}}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("empty thread name: %v", err)
	}
	if _, err := New(Config{Threads: []ThreadConfig{{Name: "A", Target: NewTable("b")},
		{Name: "A", Target: NewTable("c")}}}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("duplicate thread: %v", err)
	}
}
