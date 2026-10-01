package weightedvote

import "sort"

// State 是单个节点持有的投票状态副本。
// 同一投票人的票与权重各自构成 LWW-Register，按因果时间戳取最新值。
type State struct {
	nodeID  string
	clock   uint64
	votes   map[string]VoteRecord
	weights map[string]WeightRecord
}

// NewState 创建一个节点状态副本，nodeID 在全网需唯一（用于并发决胜）。
func NewState(nodeID string) *State {
	return &State{
		nodeID:  nodeID,
		votes:   make(map[string]VoteRecord),
		weights: make(map[string]WeightRecord),
	}
}

// next 推进本地 Lamport 时钟并生成新事件的因果时间戳。
func (s *State) next() Timestamp {
	s.clock++
	return Timestamp{Lamport: s.clock, Node: s.nodeID}
}

// SetWeight 因果地调整某投票人的权重（动态权重变更）。
func (s *State) SetWeight(voter string, weight uint64) {
	s.weights[voter] = WeightRecord{Weight: weight, Time: s.next()}
}

// CastVote 以某投票人身份投出一票（赞成/反对/弃权）。
func (s *State) CastVote(voter string, choice Vote) {
	s.votes[voter] = VoteRecord{Choice: choice, Time: s.next()}
}

// Merge 将另一个副本的状态合并进本副本。
//
// 对每个投票人的票与权重分别取因果时间戳较新的记录；
// 时间戳相同则记录必然相同（幂等）。合并满足交换律、结合律、幂等律。
func (s *State) Merge(o *State) {
	for voter, rec := range o.votes {
		if cur, ok := s.votes[voter]; !ok || rec.Time.Compare(cur.Time) > 0 {
			s.votes[voter] = rec
		}
	}
	for voter, rec := range o.weights {
		if cur, ok := s.weights[voter]; !ok || rec.Time.Compare(cur.Time) > 0 {
			s.weights[voter] = rec
		}
	}
	if o.clock > s.clock {
		s.clock = o.clock
	}
}

// Clone 返回本副本的深拷贝（用于模拟网络分区后各自独立演化）。
func (s *State) Clone() *State {
	c := NewState(s.nodeID)
	c.clock = s.clock
	for voter, rec := range s.votes {
		c.votes[voter] = rec
	}
	for voter, rec := range s.weights {
		c.weights[voter] = rec
	}
	return c
}

// VoteOf 返回某投票人当前的票及其时间戳。
func (s *State) VoteOf(voter string) (VoteRecord, bool) {
	rec, ok := s.votes[voter]
	return rec, ok
}

// WeightOf 返回某投票人当前的权重及其时间戳。
func (s *State) WeightOf(voter string) (WeightRecord, bool) {
	rec, ok := s.weights[voter]
	return rec, ok
}

// Voters 返回所有已知投票人（出现过票或权重记录），按字典序排序以保证确定性输出。
func (s *State) Voters() []string {
	seen := make(map[string]struct{}, len(s.weights))
	for voter := range s.weights {
		seen[voter] = struct{}{}
	}
	for voter := range s.votes {
		seen[voter] = struct{}{}
	}
	voters := make([]string, 0, len(seen))
	for voter := range seen {
		voters = append(voters, voter)
	}
	sort.Strings(voters)
	return voters
}

// Tally 汇总当前状态：统计全网有效总权重及各类票的权重。
// 只有既投了票又有权重记录的投票人才计入有效权重。
func (s *State) Tally() Tally {
	var t Tally
	for _, voter := range s.Voters() {
		wrec, wok := s.weights[voter]
		vrec, vok := s.votes[voter]
		if !wok || !vok {
			continue
		}
		t.Voters++
		t.TotalWeight += wrec.Weight
		switch vrec.Choice {
		case Yes:
			t.YesWeight += wrec.Weight
		case No:
			t.NoWeight += wrec.Weight
		default:
			t.AbstainWeight += wrec.Weight
		}
	}
	return t
}

// Fork 返回本副本的深拷贝，并以新的节点 ID 继续独立演化。
// 用于模拟网络分区：分区两侧从同一状态分叉，各自产生带唯一
// 节点 ID 的因果时间戳，保证愈合后合并结果唯一确定。
func (s *State) Fork(nodeID string) *State {
	c := s.Clone()
	c.nodeID = nodeID
	return c
}
