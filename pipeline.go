package typepipe

import "fmt"

// Pipeline 按字段映射规则逐条转换记录，并汇总数据清洗异常日志。
type Pipeline struct {
	fields []*Field
}

func NewPipeline(fields ...*Field) (*Pipeline, error) {
	for _, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("typepipe: field with empty name")
		}
		if _, ok := converters[f.Converter]; !ok {
			return nil, fmt.Errorf("typepipe: unknown converter %q for field %q", f.Converter, f.Name)
		}
	}
	return &Pipeline{fields: fields}, nil
}

// Process 转换单条记录，返回目标记录与该记录产生的异常日志。
func (p *Pipeline) Process(rec map[string]any) (map[string]any, []Anomaly) {
	out := make(map[string]any, len(p.fields))
	var logs []Anomaly
	for _, f := range p.fields {
		v, ok := rec[f.Name]
		if !ok {
			v = nil
		}
		cv, anom := converters[f.Converter](f, v)
		out[f.Name] = cv
		if anom != nil {
			logs = append(logs, *anom)
		}
	}
	return out, logs
}

// ProcessBatch 批量转换，异常日志按记录顺序汇总。
func (p *Pipeline) ProcessBatch(recs []map[string]any) ([]map[string]any, []Anomaly) {
	out := make([]map[string]any, 0, len(recs))
	var logs []Anomaly
	for _, rec := range recs {
		o, l := p.Process(rec)
		out = append(out, o)
		logs = append(logs, l...)
	}
	return out, logs
}
