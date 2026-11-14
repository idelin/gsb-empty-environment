package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"
)

var (
	collected = time.Date(2026, 11, 14, 8, 30, 0, 0, time.UTC)
	items     = []string{"GLU", "HbA1c"}
	results   = []Result{{Item: "GLU", Value: "5.6", Unit: "mmol/L"}}
)

func setup(t *testing.T) (*Binder, *MemoryInstrumentStore, *Publisher, ed25519.PublicKey) {
	t.Helper()
	inst := NewMemoryInstrumentStore()
	binder := NewBinder(inst)
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return binder, inst, NewPublisher(binder, priv), pub
}

func bindAndRecord(t *testing.T, binder *Binder, inst *MemoryInstrumentStore, barcode, patient string) {
	t.Helper()
	if _, err := binder.Bind(barcode, patient, collected, items); err != nil {
		t.Fatalf("bind: %v", err)
	}
	inst.Put(InstrumentRecord{
		Barcode: barcode, PatientID: patient, CollectedAt: collected, Items: items, RawRef: "raw/" + barcode,
	})
}

// 合法发布：绑定、仪器记录、签名、归属校验全部通过。
func TestLegitimatePublish(t *testing.T) {
	binder, inst, publisher, pub := setup(t)
	bindAndRecord(t, binder, inst, "BC001", "patient-A")

	rep, err := publisher.Publish("BC001", "patient-A", collected, items, results)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if rep.Version != ReportVersion {
		t.Fatalf("version = %d", rep.Version)
	}
	if err := VerifyForPatient(rep, binder, pub, "patient-A"); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got := len(binder.History("BC001")); got != 1 {
		t.Fatalf("history len = %d", got)
	}
}

// 条码替换：攻击者把他人报告字段改挂到自己账户或伪造绑定，发布与验签均失败。
func TestBarcodeSubstitution(t *testing.T) {
	binder, inst, publisher, pub := setup(t)
	bindAndRecord(t, binder, inst, "BC001", "patient-A")
	bindAndRecord(t, binder, inst, "BC002", "patient-B")

	// 攻击者用他人条码 BC001 给自己的患者身份发布。
	if _, err := publisher.Publish("BC001", "patient-B", collected, items, results); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("want ErrBindingMismatch, got %v", err)
	}
	// 攻击者篡改仪器原始记录也过不了发布前核对。
	inst.Put(InstrumentRecord{Barcode: "BC002", PatientID: "patient-A", CollectedAt: collected, Items: items})
	if _, err := publisher.Publish("BC002", "patient-B", collected, items, results); !errors.Is(err, ErrInstrumentMismatch) {
		t.Fatalf("want ErrInstrumentMismatch, got %v", err)
	}
	// 合法报告被改条码后验签失败。
	inst.Put(InstrumentRecord{Barcode: "BC002", PatientID: "patient-B", CollectedAt: collected, Items: items})
	rep, err := publisher.Publish("BC002", "patient-B", collected, items, results)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	forged := *rep
	forged.Barcode = "BC001"
	forged.PatientID = "patient-B"
	if err := Verify(&forged, binder, pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
}

// 报告复制：攻击者原样复制他人已签名报告，无法挂到自己账户。
func TestReportCopy(t *testing.T) {
	binder, inst, publisher, pub := setup(t)
	bindAndRecord(t, binder, inst, "BC001", "patient-A")

	rep, err := publisher.Publish("BC001", "patient-A", collected, items, results)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	// 复制的报告仍然锚定原患者，挂到攻击者账户被拒绝。
	copied := *rep
	if err := VerifyForPatient(&copied, binder, pub, "patient-B"); !errors.Is(err, ErrWrongPatient) {
		t.Fatalf("want ErrWrongPatient, got %v", err)
	}
	// 只改患者字段重挂，签名立即失效。
	copied.PatientID = "patient-B"
	if err := Verify(&copied, binder, pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	// 原报告对原患者仍然有效。
	if err := VerifyForPatient(rep, binder, pub, "patient-A"); err != nil {
		t.Fatalf("original report must stay valid: %v", err)
	}
}

// 时间冲突与条码复用：同条码不同采集时间、跨患者、项目不一致均被拒绝。
func TestBindingConflicts(t *testing.T) {
	binder, _, _, _ := setup(t)
	if _, err := binder.Bind("BC001", "patient-A", collected, items); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 幂等重复登记允许。
	if _, err := binder.Bind("BC001", "patient-A", collected, items); err != nil {
		t.Fatalf("idempotent rebind: %v", err)
	}
	// 跨患者复用。
	if _, err := binder.Bind("BC001", "patient-B", collected, items); !errors.Is(err, ErrCrossPatient) {
		t.Fatalf("want ErrCrossPatient, got %v", err)
	}
	// 同患者但采集时间冲突。
	later := collected.Add(2 * time.Hour)
	if _, err := binder.Bind("BC001", "patient-A", later, items); !errors.Is(err, ErrTimeConflict) {
		t.Fatalf("want ErrTimeConflict, got %v", err)
	}
	// 同患者同时间但项目不一致。
	if _, err := binder.Bind("BC001", "patient-A", collected, []string{"K", "Na"}); !errors.Is(err, ErrBarcodeReuse) {
		t.Fatalf("want ErrBarcodeReuse, got %v", err)
	}
	if got := len(binder.History("BC001")); got != 1 {
		t.Fatalf("history len = %d, want 1", got)
	}
}

// 签名与版本固定：结果只属原样本，版本降级、结果篡改均验签失败。
func TestSignatureAndVersionPinning(t *testing.T) {
	binder, inst, publisher, pub := setup(t)
	bindAndRecord(t, binder, inst, "BC001", "patient-A")

	rep, err := publisher.Publish("BC001", "patient-A", collected, items, results)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := Verify(rep, binder, pub); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// 版本降级被拒绝（版本号在签名内，无法改回合法版本）。
	downgraded := *rep
	downgraded.Version = 0
	if err := Verify(&downgraded, binder, pub); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
	// 篡改检验结果。
	tampered := *rep
	tampered.Results = []Result{{Item: "GLU", Value: "11.1", Unit: "mmol/L"}}
	if err := Verify(&tampered, binder, pub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature, got %v", err)
	}
	// 错误的公钥。
	otherPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(rep, binder, otherPub); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("want ErrBadSignature with wrong key, got %v", err)
	}
	// 未登记的条码。
	ghost := *rep
	ghost.Barcode = "BC999"
	if err := Verify(&ghost, binder, pub); !errors.Is(err, ErrBadSignature) && !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("want signature/binding failure, got %v", err)
	}
}
