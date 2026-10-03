package saga

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

// recorder 以线程安全的方式记录业务动作/补偿动作的执行顺序。
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.calls = append(r.calls, s)
	r.mu.Unlock()
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// procurementDef 构造验收场景的长采购事务：审批 -> 采购 -> 付款。
// 付款步骤向银行发起异步请求后返回 ErrWait 进入等待。
func procurementDef(rec *recorder, payTimeout time.Duration) *SagaDef {
	return &SagaDef{
		Name: "procurement",
		Steps: []Step{
			{
				Name: "approve",
				Action: func(ctx *Context) error {
					rec.add("approve")
					ctx.Data["approver"] = "manager-zhang"
					return nil
				},
				Compensate: func(ctx *Context) error {
					rec.add("notify-approver")
					return nil
				},
			},
			{
				Name: "purchase",
				Action: func(ctx *Context) error {
					rec.add("purchase")
					ctx.Data["po"] = "PO-1001"
					return nil
				},
				Compensate: func(ctx *Context) error {
					rec.add("cancel-purchase")
					return nil
				},
			},
			{
				Name:    "pay",
				Timeout: payTimeout,
				Action: func(ctx *Context) error {
					rec.add("pay-request")
					return ErrWait // 等待银行异步回执
				},
			},
		},
	}
}

func newEngine(t *testing.T, monitorInterval time.Duration) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return NewEngine(store, WithMonitorInterval(monitorInterval)), dir
}

func auditTypes(t *testing.T, eng *Engine, id string) []string {
	t.Helper()
	trail, err := eng.AuditTrail(id)
	if err != nil {
		t.Fatalf("AuditTrail: %v", err)
	}
	var types []string
	for _, ev := range trail {
		types = append(types, ev.Type)
	}
	return types
}

// 验收主场景：付款等待银行超时 -> 监控器唤醒 -> 逆向取消采购单并通知审批人，
// 全链路留下审计轨迹。
func TestPaymentTimeoutTriggersCompensation(t *testing.T) {
	eng, _ := newEngine(t, 10*time.Millisecond)
	rec := &recorder{}
	eng.Register(procurementDef(rec, 50*time.Millisecond))
	eng.StartMonitor()
	defer eng.Stop()

	id, err := eng.Start("procurement")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !eng.Wait(id, 3*time.Second) {
		t.Fatal("instance did not reach terminal state")
	}

	inst, err := eng.Instance(id)
	if err != nil {
		t.Fatalf("Instance: %v", err)
	}
	if inst.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", inst.Status)
	}

	// 正向：审批、采购、付款请求；逆向：先取消采购单，再通知审批人。
	want := []string{"approve", "purchase", "pay-request", "cancel-purchase", "notify-approver"}
	if got := rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}

	// 付款步骤因超时失败，且未被补偿（付款从未成功，无需退款）。
	if inst.Steps[2].Status != StatusFailed {
		t.Fatalf("pay step status = %s, want failed", inst.Steps[2].Status)
	}
	if inst.Steps[2].Compensated {
		t.Fatal("pay step must not be compensated")
	}

	// 审计轨迹：包含超时事件，且补偿按逆序发生。
	types := auditTypes(t, eng, id)
	wantTypes := []string{
		"instance_created",
		"step_started", "step_completed", // approve
		"step_started", "step_completed", // purchase
		"step_started", "step_waiting", // pay 进入等待
		"step_timeout", // 监控器判定超时
		"compensation_started",
		"step_compensated", "step_compensated", // purchase、approve 逆序补偿
		"instance_compensated",
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Fatalf("audit types = %v, want %v", types, wantTypes)
	}
	trail, _ := eng.AuditTrail(id)
	var compensated []string
	for _, ev := range trail {
		if ev.Type == "step_compensated" {
			compensated = append(compensated, ev.Step)
		}
	}
	if !reflect.DeepEqual(compensated, []string{"purchase", "approve"}) {
		t.Fatalf("compensation order = %v, want [purchase approve]", compensated)
	}
}

// 正常路径：银行按时回执，事务全部完成。
func TestHappyPathExternalCallback(t *testing.T) {
	eng, _ := newEngine(t, 10*time.Millisecond)
	rec := &recorder{}
	eng.Register(procurementDef(rec, time.Minute))
	eng.StartMonitor()
	defer eng.Stop()

	id, err := eng.Start("procurement")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	inst, _ := eng.Instance(id)
	if inst.Steps[2].Status != StatusWaiting {
		t.Fatalf("pay status = %s, want waiting", inst.Steps[2].Status)
	}

	if err := eng.Complete(id, "pay", "bank-txn-42"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	inst, _ = eng.Instance(id)
	if inst.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", inst.Status)
	}
	if inst.Steps[2].Result != "bank-txn-42" {
		t.Fatalf("pay result = %q", inst.Steps[2].Result)
	}
	if got := rec.all(); !reflect.DeepEqual(got, []string{"approve", "purchase", "pay-request"}) {
		t.Fatalf("calls = %v", got)
	}

	// 重复回调必须被拒绝且不产生任何副作用（幂等）。
	if err := eng.Complete(id, "pay", "bank-txn-43"); err == nil {
		t.Fatal("duplicate Complete should fail")
	}
	inst, _ = eng.Instance(id)
	if inst.Steps[2].Result != "bank-txn-42" {
		t.Fatalf("result overwritten by duplicate callback: %q", inst.Steps[2].Result)
	}
}

// 外部拒绝：银行明确拒绝付款 -> 立即触发逆向补偿。
func TestExternalRejectionTriggersCompensation(t *testing.T) {
	eng, _ := newEngine(t, 10*time.Millisecond)
	rec := &recorder{}
	eng.Register(procurementDef(rec, time.Minute)) // 超时很长，排除超时干扰
	eng.StartMonitor()
	defer eng.Stop()

	id, _ := eng.Start("procurement")
	if err := eng.Fail(id, "pay", "bank rejected: insufficient funds"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	inst, _ := eng.Instance(id)
	if inst.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", inst.Status)
	}
	want := []string{"approve", "purchase", "pay-request", "cancel-purchase", "notify-approver"}
	if got := rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	found := false
	trail, _ := eng.AuditTrail(id)
	for _, ev := range trail {
		if ev.Type == "step_rejected" && ev.Step == "pay" {
			found = true
		}
	}
	if !found {
		t.Fatal("audit trail missing step_rejected event")
	}
}

// 断电恢复：等待中的实例在进程重启后恢复，已完成步骤不重跑、
// 后续步骤不遗漏，每个动作全程恰好执行一次。
func TestCrashRecoveryNoDuplicateNoLoss(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}

	// 第一次"进程"：执行到付款等待时模拟断电（直接丢弃引擎）。
	eng1 := NewEngine(store)
	eng1.Register(procurementDef(rec, time.Minute))
	id, err := eng1.Start("procurement")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := rec.all(); !reflect.DeepEqual(got, []string{"approve", "purchase", "pay-request"}) {
		t.Fatalf("before crash calls = %v", got)
	}

	// 第二次"进程"：同一存储目录，重新注册定义并恢复。
	eng2 := NewEngine(store)
	eng2.Register(procurementDef(rec, time.Minute))
	if err := eng2.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// 恢复后已完成步骤绝不重跑。
	if got := rec.all(); !reflect.DeepEqual(got, []string{"approve", "purchase", "pay-request"}) {
		t.Fatalf("after recover calls = %v (duplicate execution!)", got)
	}
	inst, err := eng2.Instance(id)
	if err != nil {
		t.Fatalf("Instance after recover: %v", err)
	}
	if inst.Steps[2].Status != StatusWaiting {
		t.Fatalf("pay status after recover = %s, want waiting", inst.Steps[2].Status)
	}

	// 银行回执到达，事务在新进程中继续并完成。
	if err := eng2.Complete(id, "pay", "bank-txn-99"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	inst, _ = eng2.Instance(id)
	if inst.Status != StatusCompleted {
		t.Fatalf("status = %s, want completed", inst.Status)
	}
	// 每个动作全程恰好一次：不重不漏。
	if got := rec.all(); !reflect.DeepEqual(got, []string{"approve", "purchase", "pay-request"}) {
		t.Fatalf("final calls = %v", got)
	}
}

// 断电时步骤执行到一半（running 状态）：恢复后重置为 pending 并重放，
// 由幂等 Action 保证语义正确。
func TestCrashMidStepIsRetried(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	// 手工构造崩溃现场：approve 已完成，purchase 执行到一半断电。
	crashed := &Instance{
		ID:     "crash-mid-step",
		Saga:   "procurement",
		Status: StatusRunning,
		Steps: []StepState{
			{Name: "approve", Status: StatusCompleted, Attempts: 1},
			{Name: "purchase", Status: StatusRunning, Attempts: 1},
			{Name: "pay", Status: StatusPending},
		},
		Data:      map[string]string{"approver": "manager-zhang"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.SaveInstance(crashed); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	eng := NewEngine(store)
	eng.Register(procurementDef(rec, time.Minute))
	if err := eng.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// purchase 被重放，pay 进入等待；approve 未重跑。
	if got := rec.all(); !reflect.DeepEqual(got, []string{"purchase", "pay-request"}) {
		t.Fatalf("calls = %v", got)
	}
	inst, _ := eng.Instance("crash-mid-step")
	if inst.Steps[1].Attempts != 2 {
		t.Fatalf("purchase attempts = %d, want 2", inst.Steps[1].Attempts)
	}
	found := false
	trail, _ := eng.AuditTrail("crash-mid-step")
	for _, ev := range trail {
		if ev.Type == "step_retry" && ev.Step == "purchase" {
			found = true
		}
	}
	if !found {
		t.Fatal("audit trail missing step_retry event")
	}
}

// 停机期间等待超时：恢复时立即进入补偿，无需等待监控器。
func TestTimeoutDuringDowntimeRecovered(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	crashed := &Instance{
		ID:     "downtime-timeout",
		Saga:   "procurement",
		Status: StatusRunning,
		Steps: []StepState{
			{Name: "approve", Status: StatusCompleted, Attempts: 1},
			{Name: "purchase", Status: StatusCompleted, Attempts: 1},
			{Name: "pay", Status: StatusWaiting, Attempts: 1, Deadline: time.Now().Add(-time.Hour)},
		},
		Data:      map[string]string{"approver": "manager-zhang", "po": "PO-1001"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := store.SaveInstance(crashed); err != nil {
		t.Fatal(err)
	}

	rec := &recorder{}
	eng := NewEngine(store)
	eng.Register(procurementDef(rec, time.Minute))
	if err := eng.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	inst, _ := eng.Instance("downtime-timeout")
	if inst.Status != StatusCompensated {
		t.Fatalf("status = %s, want compensated", inst.Status)
	}
	if got := rec.all(); !reflect.DeepEqual(got, []string{"cancel-purchase", "notify-approver"}) {
		t.Fatalf("calls = %v", got)
	}
}

// 状态快照与审计轨迹确实落盘，可被独立进程读取复核。
func TestPersistenceOnDisk(t *testing.T) {
	eng, dir := newEngine(t, 10*time.Millisecond)
	rec := &recorder{}
	eng.Register(procurementDef(rec, 50*time.Millisecond))
	eng.StartMonitor()
	defer eng.Stop()

	id, _ := eng.Start("procurement")
	if !eng.Wait(id, 3*time.Second) {
		t.Fatal("not terminal")
	}

	// 用全新的 Store/Engine（模拟另一个进程）读取磁盘状态。
	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	insts, err := store2.LoadInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(insts) != 1 || insts[0].ID != id || insts[0].Status != StatusCompensated {
		t.Fatalf("loaded instances = %+v", insts)
	}
	trail, err := store2.AuditTrail(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) == 0 {
		t.Fatal("empty audit trail on disk")
	}
	for _, ev := range trail {
		if ev.InstanceID != id || ev.Type == "" || ev.Time.IsZero() {
			t.Fatalf("malformed audit event: %+v", ev)
		}
	}
}
