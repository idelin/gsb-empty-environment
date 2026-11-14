package netsec

import (
	"strings"
	"testing"
)

func abcd() []Fragment {
	return []Fragment{
		{Seq: 0, Data: "A"},
		{Seq: 1, Data: "B"},
		{Seq: 2, Data: "C"},
		{Seq: 3, Data: "D"},
	}
}

func TestTruncationShortest(t *testing.T) {
	frags := abcd()
	// 接收机认证每片、见末片即完整：截尾后仍被当作完整报文。
	atk, err := FindAttack(frags, NewLastFragmentReceiver(3), 0)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if atk == nil {
		t.Fatal("期望找到截尾攻击")
	}
	// 最短见证：删除前三片，仅重放末片。
	if len(atk.Sequence) != 1 || atk.Sequence[0].Seq != 3 {
		t.Fatalf("最短攻击应为仅含末片的长度 1 序列, 得到 %+v", atk.Sequence)
	}
	if atk.Received != "D" || atk.Sent != "ABCD" {
		t.Fatalf("内容不符: sent=%q received=%q", atk.Sent, atk.Received)
	}
	if err := Verify(frags, NewLastFragmentReceiver(3), atk); err != nil {
		t.Fatalf("证书验证失败: %v", err)
	}
	cert := atk.Certificate()
	if !strings.Contains(cert, "未伪造") || !strings.Contains(cert, `"D"`) {
		t.Fatalf("证书文本缺少依据: %s", cert)
	}
}

func TestReorderAttack(t *testing.T) {
	frags := abcd()[:2] // A,B
	// 任意次序接收两片即完整。
	fsm := &FSM{
		Start:  0,
		Accept: map[int]bool{3: true},
		Delta: map[Transition]int{
			{State: 0, Seq: 0}: 1,
			{State: 0, Seq: 1}: 2,
			{State: 1, Seq: 1}: 3,
			{State: 2, Seq: 0}: 3,
		},
	}
	atk, err := FindAttack(frags, fsm, 0)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if atk == nil {
		t.Fatal("期望找到重排攻击")
	}
	if len(atk.Sequence) != 2 || atk.Received != "BA" {
		t.Fatalf("期望长度 2 的重排攻击 BA, 得到 %+v received=%q", atk.Sequence, atk.Received)
	}
	if err := Verify(frags, fsm, atk); err != nil {
		t.Fatalf("证书验证失败: %v", err)
	}
}

func TestDuplicateAttack(t *testing.T) {
	frags := abcd()
	// 收满 4 片即完整，不校验序号：重复片段可冒充完整报文。
	fsm := NewCountingReceiver(4, []int{0, 1, 2, 3})
	atk, err := FindAttack(frags, fsm, 0)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if atk == nil {
		t.Fatal("期望找到重复攻击")
	}
	if len(atk.Sequence) != 4 || atk.Received != "AAAA" {
		t.Fatalf("期望长度 4 的重复攻击 AAAA, 得到 %+v received=%q", atk.Sequence, atk.Received)
	}
	if err := Verify(frags, fsm, atk); err != nil {
		t.Fatalf("证书验证失败: %v", err)
	}
}

func TestOrderedReceiverResists(t *testing.T) {
	frags := abcd()
	atk, err := FindAttack(frags, NewOrderedReceiver(3), 0)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if atk != nil {
		t.Fatalf("严格按序接收机不应被攻破, 得到 %+v", atk.Sequence)
	}
}

func TestVerifyRejectsForgery(t *testing.T) {
	frags := abcd()
	fsm := NewLastFragmentReceiver(3)
	// 证书声称末片来自发送片段 0，来源与实际片段不符。
	forged := &Attack{
		Sequence: []Fragment{{Seq: 3, Data: "D"}},
		Sources:  []int{0},
		Sent:     "ABCD",
		Received: "D",
	}
	if err := Verify(frags, fsm, forged); err == nil {
		t.Fatal("伪造来源的证书应被拒绝")
	}
	// 证书包含发送集合之外的片段。
	alien := &Attack{
		Sequence: []Fragment{{Seq: 3, Data: "X"}},
		Sources:  []int{3},
		Sent:     "ABCD",
		Received: "X",
	}
	if err := Verify(frags, fsm, alien); err == nil {
		t.Fatal("含集合外片段的证书应被拒绝")
	}
}

func TestFragmentLimit(t *testing.T) {
	frags := make([]Fragment, MaxFragments+1)
	for i := range frags {
		frags[i] = Fragment{Seq: i, Data: "x"}
	}
	if _, err := FindAttack(frags, NewLastFragmentReceiver(MaxFragments), 0); err == nil {
		t.Fatal("超过八片应报错")
	}
}

func TestIdenticalContentAcceptedIsNotAttack(t *testing.T) {
	// 重放完整原序列被接受但内容一致，不构成攻击。
	frags := abcd()
	fsm := NewCountingReceiver(4, []int{0, 1, 2, 3})
	atk, err := FindAttack(frags, fsm, 3) // 深度不足以容纳 4 片
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if atk != nil {
		t.Fatalf("深度上限内不应有攻击, 得到 %+v", atk.Sequence)
	}
}
