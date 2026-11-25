package leaklab

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// EncodeWitness 返回见证的规范 JSON 字节，供审查服务存档与重放。
func EncodeWitness(w *Witness) ([]byte, error) {
	if w == nil {
		return nil, fmt.Errorf("encode witness: nil witness")
	}
	buf, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(buf, '\n'), nil
}

// DecodeWitness 从 JSON 还原见证，并重新解析其中的程序文本。
func DecodeWitness(data []byte) (*Witness, error) {
	var w Witness
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	if w.ProgramText == "" {
		return nil, fmt.Errorf("witness missing program_text")
	}
	prog, err := ParseProgram(w.ProgramText)
	if err != nil {
		return nil, fmt.Errorf("witness program_text invalid: %w", err)
	}
	if err := w.checkConsistency(prog); err != nil {
		return nil, err
	}
	return &w, nil
}

func (w *Witness) checkConsistency(prog *Program) error {
	if err := w.Spec.Validate(); err != nil {
		return err
	}
	if int(w.SitePC) >= len(prog.Instr) || prog.Instr[w.SitePC].Op != OpJmpEq {
		return fmt.Errorf("mispredict_pc %d is not a jmpeq instruction", w.SitePC)
	}
	if err := checkView(w.A, "run_a", int(w.SitePC)); err != nil {
		return err
	}
	if err := checkView(w.B, "run_b", int(w.SitePC)); err != nil {
		return err
	}
	return nil
}

func checkView(v RunView, name string, sitePC int) error {
	if v.Result == nil {
		return fmt.Errorf("%s missing result", name)
	}
	if !v.Result.Mispredicted || v.Result.MispredictPC != sitePC {
		return fmt.Errorf("%s misprediction site mismatch: got pc=%d want %d", name, v.Result.MispredictPC, sitePC)
	}
	if len(v.Result.SpecTrace) == 0 {
		return fmt.Errorf("%s has empty speculative trace", name)
	}
	return nil
}

// Replay 重新执行见证两侧运行并比对结果，任何偏差都返回错误。
// 校验内容：公开输入一致、秘密不同、同一误预测站点、架构 probe 序列一致、
// 推测 probe 序列不同，以及回滚后架构寄存器/内存与见证快照逐字节相同。
func (w *Witness) Replay() error {
	prog, err := ParseProgram(w.ProgramText)
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	if err := w.checkConsistency(prog); err != nil {
		return err
	}
	if !bytes.Equal(w.A.Input.Public, w.B.Input.Public) {
		return fmt.Errorf("replay: public inputs differ: %v vs %v", w.A.Input.Public, w.B.Input.Public)
	}
	if w.A.Input.Secret == w.B.Input.Secret {
		return fmt.Errorf("replay: secret values are equal (%d)", w.A.Input.Secret)
	}
	a, err := Run(prog, w.Spec, w.A.Input, RunOptions{MispredictPC: w.A.MispredictPC})
	if err != nil {
		return fmt.Errorf("replay run_a: %w", err)
	}
	b, err := Run(prog, w.Spec, w.B.Input, RunOptions{MispredictPC: w.B.MispredictPC})
	if err != nil {
		return fmt.Errorf("replay run_b: %w", err)
	}
	if err := compareReplay("run_a", a, w.A.Result); err != nil {
		return err
	}
	if err := compareReplay("run_b", b, w.B.Result); err != nil {
		return err
	}
	if !probeEqual(a.ArchTrace, b.ArchTrace) {
		return fmt.Errorf("replay: architectural probe traces differ")
	}
	if probeEqual(a.SpecTrace, b.SpecTrace) {
		return fmt.Errorf("replay: speculative probe traces are equal")
	}
	return nil
}

func compareReplay(name string, got, want *RunResult) error {
	if got.Status != want.Status {
		return fmt.Errorf("%s status: got %q want %q", name, got.Status, want.Status)
	}
	if got.MispredictPC != want.MispredictPC || got.Mispredicted != want.Mispredicted {
		return fmt.Errorf("%s misprediction metadata mismatch", name)
	}
	if !probeEqual(got.ArchTrace, want.ArchTrace) || !probeEqual(got.SpecTrace, want.SpecTrace) {
		return fmt.Errorf("%s probe traces mismatch", name)
	}
	if got.Regs != want.Regs {
		return fmt.Errorf("%s architectural registers mismatch after rollback", name)
	}
	if got.Mem != want.Mem {
		return fmt.Errorf("%s architectural memory mismatch after rollback", name)
	}
	if len(got.Steps) != len(want.Steps) {
		return fmt.Errorf("%s step record length mismatch: got %d want %d", name, len(got.Steps), len(want.Steps))
	}
	for i := range got.Steps {
		if got.Steps[i] != want.Steps[i] {
			return fmt.Errorf("%s step record %d mismatch:\n got %+v\nwant %+v", name, i, got.Steps[i], want.Steps[i])
		}
	}
	return nil
}
