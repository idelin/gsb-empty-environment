package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

type party struct {
	id  string
	pub ed25519.PublicKey
	prv ed25519.PrivateKey
}

func newParty(t *testing.T, id string) party {
	t.Helper()
	pub, prv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key for %s: %v", id, err)
	}
	return party{id: id, pub: pub, prv: prv}
}

// fixture 构建一条合法汇款指令与已注册各方公钥的注册表。
func fixture(t *testing.T) (Instruction, party, []party, *KeyRegistry) {
	t.Helper()
	origin := newParty(t, "origin-bank")
	hops := []party{
		origin, // 发起节点以 ORIGINATE 记录首跳
		newParty(t, "intermediary-1"),
		newParty(t, "intermediary-2"),
		newParty(t, "beneficiary-bank"),
	}
	reg := NewKeyRegistry()
	reg.Register("origin-bank", origin.pub)
	for _, h := range hops {
		reg.Register(h.id, h.pub)
	}
	in := Instruction{
		InstructionID:   "REM-2026-0001",
		Originator:      "Alice",
		OriginatorBank:  "ORIGCNBJ",
		BeneficiaryBank: "BENECNBJ",
		BeneficiaryAcct: "6222020200112233",
		BeneficiaryName: "Bob",
		Currency:        "CNY",
		AmountCents:     1250000,
		FeeMode:         FeeOUR,
		ValueDate:       "2026-11-14",
	}
	return in, origin, hops, reg
}

// forward 模拟节点间链式转发：序列化 -> 反序列化 -> 节点处理。
func forward(t *testing.T, m *Message, reg *KeyRegistry, node party, action string) (*Message, error) {
	t.Helper()
	raw, err := Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	next, err := Unmarshal(raw)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := ProcessHop(next, reg, node.id, action, node.prv); err != nil {
		return nil, err
	}
	return next, nil
}

func TestLegitimateRemittance(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	actions := []string{"ORIGINATE", "FORWARD", "FORWARD", "DELIVER"}
	for i, h := range hops {
		msg, err = forward(t, msg, reg, h, actions[i])
		if err != nil {
			t.Fatalf("hop %d (%s) rejected legitimate message: %v", i, h.id, err)
		}
	}
	if err := FinalVerify(msg, reg); err != nil {
		t.Fatalf("final verify: %v", err)
	}
	if len(msg.Hops) != 4 {
		t.Fatalf("expected 4 hop records, got %d", len(msg.Hops))
	}
	if msg.Instruction.BeneficiaryAcct != in.BeneficiaryAcct ||
		msg.Instruction.BeneficiaryBank != in.BeneficiaryBank ||
		msg.Instruction.AmountCents != in.AmountCents ||
		msg.Instruction.FeeMode != in.FeeMode {
		t.Fatal("instruction altered after multi-hop transit")
	}
	t.Logf("hop trail: %v", HopTrail(msg))
}

func TestIntermediaryRewriteBeneficiary(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// 第一跳正常。
	msg, err = forward(t, msg, reg, hops[0], "ORIGINATE")
	if err != nil {
		t.Fatalf("hop 0: %v", err)
	}
	// 攻击者在转发前改写收款行与账号。
	msg.Instruction.BeneficiaryBank = "EVILBANK"
	msg.Instruction.BeneficiaryAcct = "999999999999"
	// 下一跳必须拒绝并退回。
	if _, err := forward(t, msg, reg, hops[1], "FORWARD"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature after rewrite, got %v", err)
	}
	// 收款行入账前同样必须拒绝。
	if err := FinalVerify(msg, reg); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("final verify must reject rewritten message, got %v", err)
	}
}

func TestSignatureStripped(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// 攻击者剥离签名，企图以未签名报文通过。
	msg.Signature = nil
	if _, err := forward(t, msg, reg, hops[0], "ORIGINATE"); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("expected ErrMissingSignature, got %v", err)
	}
	if err := FinalVerify(msg, reg); !errors.Is(err, ErrMissingSignature) {
		t.Fatalf("final verify must reject stripped signature, got %v", err)
	}
}

func TestAmountTampered(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	msg, err = forward(t, msg, reg, hops[0], "ORIGINATE")
	if err != nil {
		t.Fatalf("hop 0: %v", err)
	}
	// 篡改金额与费用承担方式。
	msg.Instruction.AmountCents *= 100
	msg.Instruction.FeeMode = FeeBEN
	if _, err := forward(t, msg, reg, hops[1], "FORWARD"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature after amount tamper, got %v", err)
	}
}

func TestMultiHopFundsReachOnlyOriginalBeneficiary(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	actions := []string{"ORIGINATE", "FORWARD", "FORWARD", "DELIVER"}
	for i, h := range hops {
		msg, err = forward(t, msg, reg, h, actions[i])
		if err != nil {
			t.Fatalf("hop %d: %v", i, err)
		}
	}
	// 多跳完整验证通过。
	if err := FinalVerify(msg, reg); err != nil {
		t.Fatalf("final verify: %v", err)
	}
	// 收款行只按验签通过的指令入账：模拟入账目标。
	creditBank := msg.Instruction.BeneficiaryBank
	creditAcct := msg.Instruction.BeneficiaryAcct
	if creditBank != "BENECNBJ" || creditAcct != "6222020200112233" {
		t.Fatalf("funds routed to %s/%s, not original beneficiary", creditBank, creditAcct)
	}
	// 任何一跳记录被伪造（如重排）也必须被最终核验发现。
	msg.Hops[1], msg.Hops[2] = msg.Hops[2], msg.Hops[1]
	if err := FinalVerify(msg, reg); !errors.Is(err, ErrBrokenHopChain) {
		t.Fatalf("expected ErrBrokenHopChain after hop reorder, got %v", err)
	}
}

func TestTamperedHopRecordDigest(t *testing.T) {
	in, origin, hops, reg := fixture(t)
	msg, err := Sign(in, "origin-bank", origin.prv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	msg, err = forward(t, msg, reg, hops[0], "ORIGINATE")
	if err != nil {
		t.Fatalf("hop 0: %v", err)
	}
	// 攻击者伪造跳记录中的报文摘要。
	msg.Hops[0].MsgDigest = "00" + msg.Hops[0].MsgDigest[2:]
	if _, err := forward(t, msg, reg, hops[1], "FORWARD"); err == nil {
		t.Fatal("expected rejection of forged hop record")
	}
}
