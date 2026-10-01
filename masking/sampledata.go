package masking

import _ "embed"

//go:embed policy.json
var DefaultPolicyJSON []byte

// SampleRoster 是示例员工名册。
func SampleRoster() []Record {
	return []Record{
		{"emp_id": "E1001", "name": "张伟", "department": "研发部", "phone": "13812345678", "id_card": "110101199003077788", "salary": 23500.0},
		{"emp_id": "E1002", "name": "李娜", "department": "财务部", "phone": "13987654321", "id_card": "310104198811223344", "salary": 18200.0},
		{"emp_id": "E1003", "name": "王强", "department": "研发部", "phone": "13755556666", "id_card": "440305199505126655", "salary": 27800.0},
		{"emp_id": "E1004", "name": "张伟", "department": "市场部", "phone": "13611112222", "id_card": "330106199201019999", "salary": 15400.0},
	}
}
