package compliance

import (
	"math"
	"strings"
	"testing"
)

func testRoster() []Employee {
	return []Employee{
		{ID: "E001", Name: "张伟", Dept: "研发", Title: "高级工程师", Phone: "13812345678", IDCard: "110101199003077788", Salary: 23000},
		{ID: "E002", Name: "李娜", Dept: "财务", Title: "会计", Phone: "13998765432", IDCard: "310104198511123456", Salary: 14500},
		{ID: "E003", Name: "张伟", Dept: "研发", Title: "测试工程师", Phone: "13700001111", IDCard: "440305199912010000", Salary: 12000},
	}
}

// TestMask 掩码转换：保留前 3 后 4，短串全掩码。
func TestMask(t *testing.T) {
	ctx := &Context{}
	m := Mask{Head: 3, Tail: 4}
	if got := m.Apply("13812345678", ctx); got != "138****5678" {
		t.Fatalf("phone mask = %v, want 138****5678", got)
	}
	idm := Mask{Head: 6, Tail: 4}
	got := idm.Apply("110101199003077788", ctx).(string)
	if !strings.HasPrefix(got, "110101") || !strings.HasSuffix(got, "7788") || strings.Contains(got[6:len(got)-4], "9") {
		t.Fatalf("id_card mask = %q", got)
	}
	if got := m.Apply("12345", ctx); got != "*****" {
		t.Fatalf("short string mask = %v, want *****", got)
	}
}

// TestHashPseudonymConsistency 假名化：导出内一致，跨导出不可关联。
func TestHashPseudonymConsistency(t *testing.T) {
	roster := testRoster()
	pol := DefaultPolicies()[RoleContractor]

	exp1 := NewExporter([]byte("export-key-2026Q3"))
	recs := exp1.Export(roster, pol)
	// 两位同名“张伟”在同一次导出中假名必须一致（关联一致性）。
	if recs[0]["name"] != recs[2]["name"] {
		t.Fatalf("same name must map to same pseudonym within one export: %v vs %v",
			recs[0]["name"], recs[2]["name"])
	}
	pseudo := recs[0]["name"].(string)
	if !strings.HasPrefix(pseudo, "EMP-") || pseudo == "张伟" {
		t.Fatalf("pseudonym format invalid: %q", pseudo)
	}

	// 不同导出（不同密钥）假名不同，防止跨导出关联。
	exp2 := NewExporter([]byte("export-key-2026Q4"))
	if recs2 := exp2.Export(roster, pol); recs2[0]["name"] == pseudo {
		t.Fatalf("pseudonym must differ across exports, got identical %q", pseudo)
	}
}

// TestBucket 薪资区间分桶。
func TestBucket(t *testing.T) {
	b := Bucket{Edges: []float64{0, 10000, 15000, 20000, 25000, 30000}}
	cases := map[float64]string{
		23000: "20000-25000",
		14500: "10000-15000",
		12000: "10000-15000",
		8000:  "0-10000",
	}
	for in, want := range cases {
		if got := b.Apply(in, &Context{}); got != want {
			t.Fatalf("bucket(%v) = %v, want %v", in, got, want)
		}
	}
}

// TestLaplaceNoise 差分加噪：同密钥可复现，噪声有界于合理范围。
func TestLaplaceNoise(t *testing.T) {
	n := LaplaceNoise{Scale: 500}
	e1 := NewExporter([]byte("k"))
	e2 := NewExporter([]byte("k"))
	v1 := n.Apply(23000.0, e1.ctx).(float64)
	v2 := n.Apply(23000.0, e2.ctx).(float64)
	if v1 != v2 {
		t.Fatalf("same export key must reproduce noise: %v vs %v", v1, v2)
	}
	// 拉普拉斯噪声理论上无界，但单次采样落在 10 倍尺度内的概率 > 99.99%。
	if math.Abs(v1-23000) > 10*500 {
		t.Fatalf("noise out of reasonable bound: %v", v1)
	}
}

// TestFinancePolicy 财务角色：薪资明文，手机号前 3 后 4，身份证掩码。
func TestFinancePolicy(t *testing.T) {
	recs := NewExporter([]byte("fin")).Export(testRoster(), DefaultPolicies()[RoleFinance])
	r := recs[0]
	if r["salary"] != 23000.0 {
		t.Fatalf("finance must see exact salary, got %v", r["salary"])
	}
	if r["phone"] != "138****5678" {
		t.Fatalf("finance phone = %v, want 138****5678", r["phone"])
	}
	idc := r["id_card"].(string)
	if !strings.HasPrefix(idc, "110101") || !strings.HasSuffix(idc, "7788") {
		t.Fatalf("finance id_card = %v", idc)
	}
	if r["name"] != "张伟" {
		t.Fatalf("finance name = %v", r["name"])
	}
}

// TestContractorPolicy 外包角色：薪资区间化、姓名假名化、手机号/身份证拒绝。
func TestContractorPolicy(t *testing.T) {
	recs := NewExporter([]byte("ctr")).Export(testRoster(), DefaultPolicies()[RoleContractor])
	r := recs[0]
	if r["salary"] != "20000-25000" {
		t.Fatalf("contractor salary = %v, want 20000-25000", r["salary"])
	}
	if !strings.HasPrefix(r["name"].(string), "EMP-") {
		t.Fatalf("contractor name must be pseudonymized, got %v", r["name"])
	}
	if r["phone"] != "[REDACTED]" || r["id_card"] != "[REDACTED]" {
		t.Fatalf("contractor phone/id_card must be redacted: %v %v", r["phone"], r["id_card"])
	}
	// 原始手机号/身份证片段不得出现在任何导出字段中。
	for _, rec := range recs {
		for _, col := range Columns {
			s, _ := rec[col].(string)
			if strings.Contains(s, "13812345678") || strings.Contains(s, "19900307") {
				t.Fatalf("raw sensitive data leaked in %s=%q", col, s)
			}
		}
	}
}

// TestHRAdminPolicy HR 管理员：全字段明文。
func TestHRAdminPolicy(t *testing.T) {
	recs := NewExporter([]byte("hr")).Export(testRoster(), DefaultPolicies()[RoleHRAdmin])
	if recs[0]["phone"] != "13812345678" || recs[0]["salary"] != 23000.0 {
		t.Fatalf("hr_admin must see plaintext: %v", recs[0])
	}
}

// TestDefaultDeny 未声明字段默认拒绝。
func TestDefaultDeny(t *testing.T) {
	p := Policy{Role: "custom", Level: LevelPublic, Fields: map[string]Transform{"id": Pass{}}}
	recs := NewExporter([]byte("x")).Export(testRoster(), p)
	if recs[0]["id"] != "E001" {
		t.Fatalf("declared field must pass: %v", recs[0]["id"])
	}
	for _, col := range []string{"name", "phone", "salary"} {
		if recs[0][col] != "[REDACTED]" {
			t.Fatalf("undeclared field %s must be denied, got %v", col, recs[0][col])
		}
	}
}

// TestExportCSV CSV 导出：表头与行数正确，列顺序固定。
func TestExportCSV(t *testing.T) {
	data, err := NewExporter([]byte("csv")).ExportCSV(testRoster(), DefaultPolicies()[RoleFinance])
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 { // 表头 + 3 行
		t.Fatalf("csv lines = %d, want 4", len(lines))
	}
	if lines[0] != "id,name,dept,title,phone,id_card,salary" {
		t.Fatalf("csv header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "138****5678") {
		t.Fatalf("csv row must contain masked phone: %q", lines[1])
	}
}

// TestRoleComplianceMatrix 角色-字段合规矩阵：逐一验证导出结果完全符合策略声明。
func TestRoleComplianceMatrix(t *testing.T) {
	roster := testRoster()
	for role, pol := range DefaultPolicies() {
		recs := NewExporter([]byte("matrix-"+string(role))).Export(roster, pol)
		for i, rec := range recs {
			for _, col := range Columns {
				tr := pol.TransformFor(col)
				raw := toRow(roster[i])[col]
				switch tr.(type) {
				case Pass:
					if rec[col] != raw {
						t.Errorf("%s/%s: pass expected %v, got %v", role, col, raw, rec[col])
					}
				case Deny:
					if rec[col] != "[REDACTED]" {
						t.Errorf("%s/%s: deny expected [REDACTED], got %v", role, col, rec[col])
					}
				case Mask:
					if s, ok := rec[col].(string); !ok || s == raw {
						t.Errorf("%s/%s: mask expected transformed string, got %v", role, col, rec[col])
					}
				case HashPseudonym:
					if s, ok := rec[col].(string); !ok || !strings.HasPrefix(s, "EMP-") {
						t.Errorf("%s/%s: pseudonym expected EMP-*, got %v", role, col, rec[col])
					}
				case Bucket:
					if s, ok := rec[col].(string); !ok || !strings.Contains(s, "-") {
						t.Errorf("%s/%s: bucket expected range label, got %v", role, col, rec[col])
					}
				}
			}
		}
	}
}
