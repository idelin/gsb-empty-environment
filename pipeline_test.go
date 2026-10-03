package typepipe

import "testing"

// 验收场景：MySQL 的 datetime 与 enum 字段经预设规则转换为
// 带 UTC 偏移的 ISO 字符串与整数字典码，溢出数值被处理并输出清洗异常日志。
func TestMySQLAcceptancePipeline(t *testing.T) {
	p, err := NewPipelineFromSchema("mysql", []Column{
		{Name: "id", Type: "bigint"},
		{Name: "is_active", Type: "tinyint(1)"},
		{Name: "created_at", Type: "datetime"},
		{Name: "status", Type: "enum('active','pending','banned')"},
		{Name: "score", Type: "smallint"},
		{Name: "uid", Type: "char(36)"},
	}, Options{TZOffset: "+08:00"})
	if err != nil {
		t.Fatal(err)
	}

	recs := []map[string]any{
		{ // 干净记录
			"id": int64(1), "is_active": int64(1),
			"created_at": "2024-03-01 10:30:00", "status": "pending",
			"score": int64(90), "uid": "550e8400-e29b-41d4-a716-446655440000",
		},
		{ // 脏记录：坏时间、未知枚举、溢出数值、非法 UUID
			"id": int64(2), "is_active": int64(5),
			"created_at": "garbage", "status": "ghost",
			"score": "99999", "uid": "xyz",
		},
	}

	out, logs := p.ProcessBatch(recs)

	// 干净记录：datetime -> 带 UTC 偏移的 ISO 字符串；enum -> 整数字典码
	if out[0]["created_at"] != "2024-03-01T10:30:00+08:00" {
		t.Errorf("created_at = %v", out[0]["created_at"])
	}
	if out[0]["status"] != int64(2) {
		t.Errorf("status = %v", out[0]["status"])
	}
	if out[0]["is_active"] != true || out[0]["score"] != int64(90) {
		t.Errorf("row0 = %v", out[0])
	}

	// 脏记录：全部落到安全默认值
	if out[1]["created_at"] != "1970-01-01T00:00:00Z" {
		t.Errorf("bad created_at = %v", out[1]["created_at"])
	}
	if out[1]["status"] != int64(0) {
		t.Errorf("unknown enum = %v", out[1]["status"])
	}
	if out[1]["score"] != int64(32767) { // smallint 上界钳制
		t.Errorf("overflow score = %v", out[1]["score"])
	}
	if out[1]["uid"] != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("bad uuid = %v", out[1]["uid"])
	}

	// 清洗异常日志：脏记录 5 个字段各一条（含 tinyint 越域告警）
	if len(logs) != 5 {
		t.Fatalf("logs = %v, want 5 entries", logs)
	}
	reasons := map[string]string{}
	for _, l := range logs {
		reasons[l.Field] = l.Reason
	}
	want := map[string]string{
		"is_active":  "out_of_domain",
		"created_at": "invalid_format",
		"status":     "unknown_enum",
		"score":      "overflow",
		"uid":        "invalid_format",
	}
	for field, reason := range want {
		if reasons[field] != reason {
			t.Errorf("field %s: reason = %q, want %q", field, reasons[field], reason)
		}
	}
}

func TestPostgresPreset(t *testing.T) {
	p, err := NewPipelineFromSchema("postgres", []Column{
		{Name: "id", Type: "uuid"},
		{Name: "flag", Type: "boolean"},
		{Name: "ts", Type: "timestamp with time zone"},
		{Name: "n", Type: "integer"},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, logs := p.Process(map[string]any{
		"id":   "550E8400-E29B-41D4-A716-446655440000",
		"flag": "t", "ts": "2024-01-01 00:00:00", "n": int64(5),
	})
	if len(logs) != 0 {
		t.Fatalf("unexpected logs: %v", logs)
	}
	if out["id"] != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("uuid = %v", out["id"])
	}
	if out["ts"] != "2024-01-01T00:00:00Z" {
		t.Errorf("ts = %v", out["ts"])
	}
}

func TestJSONDocPreset(t *testing.T) {
	p, err := NewPipelineFromSchema("jsondoc", []Column{
		{Name: "name", Type: "string"},
		{Name: "age", Type: "integer"},
		{Name: "tags", Type: "array"},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, logs := p.Process(map[string]any{
		"name": "alice", "age": float64(30), "tags": []any{"a", "b"},
	})
	if len(logs) != 0 {
		t.Fatalf("unexpected logs: %v", logs)
	}
	if out["age"] != int64(30) || out["name"] != "alice" {
		t.Errorf("out = %v", out)
	}
}

func TestUnsignedRange(t *testing.T) {
	p, err := NewPipelineFromSchema("mysql", []Column{{Name: "n", Type: "smallint unsigned"}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	out, logs := p.Process(map[string]any{"n": int64(70000)})
	if out["n"] != int64(65535) || len(logs) != 1 || logs[0].Reason != "overflow" {
		t.Errorf("out=%v logs=%v", out, logs)
	}
	out, logs = p.Process(map[string]any{"n": int64(-1)})
	if out["n"] != int64(0) || len(logs) != 1 {
		t.Errorf("out=%v logs=%v", out, logs)
	}
}

func TestUnknownTypeFails(t *testing.T) {
	if _, err := NewPipelineFromSchema("mysql", []Column{{Name: "x", Type: "geometry"}}, Options{}); err == nil {
		t.Error("expected error for unmapped type")
	}
}
