package compliance

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math/rand"
)

// Record 一条导出记录（列名 -> 已脱敏值）。
type Record map[string]interface{}

// Exporter 合规导出器。每次导出使用独立密钥创建，
// 保证假名与噪声仅在本次导出内关联一致。
type Exporter struct {
	ctx *Context
}

// NewExporter 以本次导出的密钥创建导出器。
// 密钥可以是随机生成的会话密钥；相同密钥可复现同一次导出（用于审计回放）。
func NewExporter(exportKey []byte) *Exporter {
	if len(exportKey) == 0 {
		exportKey = []byte("default-export-key")
	}
	return &Exporter{ctx: &Context{
		key: exportKey,
		rng: rand.New(rand.NewSource(seedFromKey(exportKey))),
	}}
}

// Export 按策略对员工名册执行声明式脱敏转换。
func (e *Exporter) Export(roster []Employee, p Policy) []Record {
	out := make([]Record, 0, len(roster))
	for _, emp := range roster {
		row := toRow(emp)
		rec := make(Record, len(Columns))
		for _, col := range Columns {
			rec[col] = p.TransformFor(col).Apply(row[col], e.ctx)
		}
		out = append(out, rec)
	}
	return out
}

// ExportJSON 导出为 JSON（带元信息头）。
func (e *Exporter) ExportJSON(roster []Employee, p Policy) ([]byte, error) {
	payload := map[string]interface{}{
		"role":    string(p.Role),
		"level":   p.Level,
		"columns": Columns,
		"records": e.Export(roster, p),
	}
	return json.MarshalIndent(payload, "", "  ")
}

// ExportCSV 导出为 CSV（列顺序固定）。
func (e *Exporter) ExportCSV(roster []Employee, p Policy) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(Columns); err != nil {
		return nil, err
	}
	for _, rec := range e.Export(roster, p) {
		row := make([]string, len(Columns))
		for i, col := range Columns {
			row[i] = fmt.Sprintf("%v", rec[col])
		}
		if err := w.Write(row); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}
