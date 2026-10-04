// Package inventory 实现多副本库存模拟器。
//
// 模型（escrow / bounded-counter）：
//   - 总库存 total 在初始化时被拆分为各副本持有的扣减额度（rights）。
//   - 副本扣减库存时只消耗自己持有的额度，离线也可安全进行，
//     因此任意时刻 sum(spent) <= total，总库存不会变负。
//   - 额度不足的副本可向其他副本请求转让：转让方先扣减自己的
//     额度并生成一条带唯一 ID 的转让消息；接收方只有在消息
//     到达后才能使用这部分额度（不得预支在途额度）。
//   - 转让消息按 ID 去重，重复投递/重复合并不会重复授予。
//   - 合并规则：两个副本交换各自已知的转让流水（按 ID 取并集），
//     对未应用过的、收款方为自己的消息补记额度。该操作满足
//     交换律、结合律、幂等律，是一个 join-semilattice。
//
// 守恒不变量：任意时刻
//
//	sum(rights_i) + sum(在途未应用的转让额) == total
//
// 全部消息投递完毕后 sum(rights_i) == total，且 sum(spent_i) <= total。
package inventory

import (
	"errors"
	"fmt"
	"sort"
)

// ErrNonPositive 表示扣减/转让数量必须为正数。
var ErrNonPositive = errors.New("inventory: amount must be positive")

// ErrInsufficient 表示副本可用额度不足。
var ErrInsufficient = errors.New("inventory: insufficient quota")

// Transfer 是一条额度转让消息。ID 全局唯一（由转让方单调序号生成），
// 用于接收方与其他副本去重。
type Transfer struct {
	ID     string
	From   string
	To     string
	Amount int64
}

func (t Transfer) String() string {
	return fmt.Sprintf("%s: %s -> %s  %d", t.ID, t.From, t.To, t.Amount)
}

// Replica 是一个库存副本，可在离线状态下独立扣减。
type Replica struct {
	ID string

	rights int64 // 当前持有的扣减额度（初始分配 + 收到转让 - 发起转让）
	spent  int64 // 已扣减数量，只增不减

	seq     int64               // 本副本产生转让消息的单调序号
	ledger  map[string]Transfer // 已知转让流水（发出、收到、转发获悉的并集）
	applied map[string]bool     // 已授予本副本的消息 ID（与“仅知晓”区分）
}

// NewReplica 创建一个持有 initial 初始额度的副本。
func NewReplica(id string, initial int64) *Replica {
	if initial < 0 {
		panic("inventory: negative initial quota")
	}
	return &Replica{
		ID:      id,
		rights:  initial,
		ledger:  make(map[string]Transfer),
		applied: make(map[string]bool),
	}
}

// Rights 返回副本当前持有的额度（含已消耗部分）。
func (r *Replica) Rights() int64 { return r.rights }

// Spent 返回副本已扣减的数量。
func (r *Replica) Spent() int64 { return r.spent }

// Available 返回副本当前可用量：持有额度减去已扣减。
func (r *Replica) Available() int64 { return r.rights - r.spent }

// Deduct 离线扣减 n 件库存。只消耗本副本持有的额度，
// 额度不足时返回 ErrInsufficient，绝不透支（保证总库存不为负）。
func (r *Replica) Deduct(n int64) error {
	if n <= 0 {
		return ErrNonPositive
	}
	if r.Available() < n {
		return fmt.Errorf("%v: replica %s has %d, want %d", ErrInsufficient, r.ID, r.Available(), n)
	}
	r.spent += n
	return nil
}

// Grant 向副本 to 转让 amount 额度。转让方立即扣减自己的额度，
// 生成一条唯一消息；接收方收到消息前无法使用该额度。
// 已消耗的额度不可转让（amount 不得超过 Available）。
func (r *Replica) Grant(to string, amount int64) (Transfer, error) {
	if amount <= 0 {
		return Transfer{}, ErrNonPositive
	}
	if r.Available() < amount {
		return Transfer{}, fmt.Errorf("%v: replica %s has %d, want to grant %d",
			ErrInsufficient, r.ID, r.Available(), amount)
	}
	r.seq++
	t := Transfer{
		ID:     fmt.Sprintf("%s#%d", r.ID, r.seq),
		From:   r.ID,
		To:     to,
		Amount: amount,
	}
	r.rights -= amount
	r.ledger[t.ID] = t
	return t, nil
}

// Receive 投递一条转让消息。消息按 ID 去重：重复投递不会重复授予。
// 注意“知晓消息”与“授予额度”分离：只有收款方是自己且尚未授予过，
// 才增加额度（自转让也必须走消息投递才生效）。
// 返回 true 表示这是一条本副本首次知晓的消息。
func (r *Replica) Receive(t Transfer) bool {
	_, known := r.ledger[t.ID]
	r.ledger[t.ID] = t
	if t.To == r.ID && !r.applied[t.ID] {
		r.applied[t.ID] = true
		r.rights += t.Amount
	}
	return !known
}

// MergeFrom 合并另一个副本的转让流水（并集），
// 对尚未应用且收款方为自己的消息补记额度。
// 幂等：重复合并不产生任何效果。
func (r *Replica) MergeFrom(other *Replica) {
	for _, id := range other.sortedTransferIDs() {
		r.Receive(other.ledger[id])
	}
}

// Ledger 返回本副本已知的转让流水，按 ID 排序，便于输出与比对。
func (r *Replica) Ledger() []Transfer {
	ids := r.sortedTransferIDs()
	out := make([]Transfer, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.ledger[id])
	}
	return out
}

func (r *Replica) sortedTransferIDs() []string {
	ids := make([]string, 0, len(r.ledger))
	for id := range r.ledger {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
