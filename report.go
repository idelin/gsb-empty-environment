package netsec

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ReportVersion 固定报告结构版本；签名覆盖版本号，降级或篡改版本即失效。
const ReportVersion = 1

var (
	ErrVersionMismatch = errors.New("netsec: unsupported report version")
	ErrBadSignature    = errors.New("netsec: report signature invalid")
	ErrWrongPatient    = errors.New("netsec: report does not belong to this patient")
)

// Result 是单个检验项目的结果。
type Result struct {
	Item  string
	Value string
	Unit  string
}

// Report 是已签名的检验报告。除 Signature 外所有字段均纳入签名。
type Report struct {
	Version     int
	ReportID    string
	Barcode     string
	PatientID   string
	CollectedAt time.Time
	Items       []string
	Results     []Result
	BindingID   string
	IssuedAt    time.Time
	Signature   []byte
}

// canonical 生成版本固定的签名原文，字段顺序与分隔符不可变。
func (r *Report) canonical() []byte {
	var sb strings.Builder
	sb.WriteString("q0460-report-v" + strconv.Itoa(r.Version) + "\x00")
	fields := []string{
		r.ReportID,
		r.Barcode,
		r.PatientID,
		r.CollectedAt.UTC().Format(time.RFC3339Nano),
		strings.Join(r.Items, ","),
		r.BindingID,
		r.IssuedAt.UTC().Format(time.RFC3339Nano),
	}
	sb.WriteString(strings.Join(fields, "\x00"))
	for _, res := range r.Results {
		sb.WriteString("\x00" + res.Item + "\x1f" + res.Value + "\x1f" + res.Unit)
	}
	return []byte(sb.String())
}

// Publisher 使用私钥签发报告，发布前经由 Binder 核对绑定与仪器记录。
type Publisher struct {
	binder *Binder
	key    ed25519.PrivateKey
	now    func() time.Time
	seq    int
}

func NewPublisher(binder *Binder, key ed25519.PrivateKey) *Publisher {
	return &Publisher{binder: binder, key: key, now: time.Now}
}

// Publish 核对样本绑定与仪器原始记录后签发报告。
// 条码被替换（字段与绑定不符）或仪器记录缺失/不一致时拒绝发布。
func (p *Publisher) Publish(barcode, patientID string, collectedAt time.Time, items []string, results []Result) (*Report, error) {
	b, err := p.binder.checkPublish(barcode, patientID, collectedAt, items)
	if err != nil {
		return nil, err
	}
	p.seq++
	rep := &Report{
		Version:     ReportVersion,
		ReportID:    fmt.Sprintf("R-%d-%d", p.now().UnixNano(), p.seq),
		Barcode:     barcode,
		PatientID:   patientID,
		CollectedAt: collectedAt.UTC(),
		Items:       append([]string(nil), items...),
		Results:     append([]Result(nil), results...),
		BindingID:   b.ID(),
		IssuedAt:    p.now().UTC(),
	}
	rep.Signature = ed25519.Sign(p.key, rep.canonical())
	return rep, nil
}

// Verify 校验报告：版本固定、签名有效、绑定摘要与注册表一致。
// 复制的报告无法改挂到其他样本或患者，因为任何字段改动都会使签名失效，
// 而原样复制时 BindingID 仍指向原样本。
func Verify(rep *Report, binder *Binder, pub ed25519.PublicKey) error {
	if rep.Version != ReportVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrVersionMismatch, rep.Version, ReportVersion)
	}
	if !ed25519.Verify(pub, rep.canonical(), rep.Signature) {
		return ErrBadSignature
	}
	b, ok := binder.Lookup(rep.Barcode)
	if !ok {
		return fmt.Errorf("%w: %s", ErrBindingNotFound, rep.Barcode)
	}
	if b.ID() != rep.BindingID {
		return fmt.Errorf("%w: binding anchor mismatch", ErrBindingMismatch)
	}
	return nil
}

// VerifyForPatient 在 Verify 之上确认报告归属指定患者账户。
func VerifyForPatient(rep *Report, binder *Binder, pub ed25519.PublicKey, patientID string) error {
	if err := Verify(rep, binder, pub); err != nil {
		return err
	}
	if rep.PatientID != patientID {
		return fmt.Errorf("%w: report belongs to %s", ErrWrongPatient, rep.PatientID)
	}
	return nil
}
