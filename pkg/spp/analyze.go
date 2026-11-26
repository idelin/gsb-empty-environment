package spp

import (
	"encoding/json"
	"fmt"
	"os"
)

// AnalysisOptions 汇总分析各阶段的规模控制参数。
type AnalysisOptions struct {
	Solve      Options
	WheelLimit int
	Repair     RepairOptions
	// SkipRepair 为真时跳过最小修复搜索。
	SkipRepair bool
}

// Report 是一次完整 SPP 分析的可序列化结果。
type Report struct {
	InstanceName      string          `json:"instanceName"`
	Destination       int             `json:"destination"`
	Outcome           Outcome         `json:"outcome"`
	OutcomeText       string          `json:"outcomeText"`
	GR                *GRReport       `json:"gaoRexford"`
	StableAssignments []Assignment    `json:"stableAssignments"`
	Truncated         bool            `json:"truncated"`
	Wheels            []*DisputeWheel `json:"disputeWheels"`
	HasDisputeWheel   bool            `json:"hasDisputeWheel"`
	Repair            *RepairPlan     `json:"repairPlan,omitempty"`
}

// LoadConfig 从 JSON 字节流加载 SPP 实例。
func LoadConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析实例 JSON 失败: %w", err)
	}
	return &cfg, nil
}

// LoadConfigFile 从 JSON 文件加载 SPP 实例。
func LoadConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取实例文件失败: %w", err)
	}
	return LoadConfig(data)
}

// Analyze 在给定实例上执行完整的稳定性与争议轮分析流水线。
func Analyze(cfg *Config, opts AnalysisOptions) (*Report, error) {
	g, err := Load(cfg)
	if err != nil {
		return nil, err
	}
	gr := analyzeGR(g)
	solved := g.Solve(opts.Solve)
	wheels := g.FindDisputeWheels(opts.WheelLimit)
	if wheels == nil {
		wheels = []*DisputeWheel{}
	}
	assignments := solved.Assignments
	if assignments == nil {
		assignments = []Assignment{}
	}

	report := &Report{
		Destination:       cfg.Destination,
		Outcome:           Classify(len(solved.Assignments)),
		OutcomeText:       outcomeText(Classify(len(solved.Assignments))),
		GR:                gr,
		StableAssignments: assignments,
		Truncated:         solved.Truncated,
		Wheels:            wheels,
		HasDisputeWheel:   len(wheels) > 0,
	}

	if !opts.SkipRepair && report.HasDisputeWheel {
		plan, err := g.MinimalRepair(cfg, gr, opts.Repair)
		if err != nil {
			return nil, fmt.Errorf("最小修复搜索失败: %w", err)
		}
		report.Repair = plan
	}
	return report, nil
}

func outcomeText(o Outcome) string {
	switch o {
	case OutcomeUnique:
		return "唯一稳态：实例安全收敛，存在且仅存在一个稳定路径分配"
	case OutcomeMultiple:
		return "多稳态：收敛结果依赖消息时序，存在非确定性分裂"
	case OutcomeNone:
		return "无稳态：不存在任何稳定路径分配，控制面必然永久震荡"
	default:
		return string(o)
	}
}

// MarshalJSON 让空选择显式序列化为 null。
func (a Assignment) MarshalJSON() ([]byte, error) {
	view := make(map[string]json.RawMessage, len(a))
	for as, p := range a {
		if p == nil {
			view[itoa(as)] = json.RawMessage("null")
			continue
		}
		raw, err := json.Marshal([]int(p))
		if err != nil {
			return nil, err
		}
		view[itoa(as)] = raw
	}
	return json.Marshal(view)
}
