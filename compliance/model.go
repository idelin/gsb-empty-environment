// Package compliance 提供声明式数据脱敏与合规导出能力。
package compliance

// Employee 员工名册记录（导出前的原始数据）。
type Employee struct {
	ID     string  // 工号
	Name   string  // 姓名
	Dept   string  // 部门
	Title  string  // 职位
	Phone  string  // 手机号
	IDCard string  // 身份证号
	Salary float64 // 月薪（元）
}

// Columns 定义导出列的固定顺序，保证输出稳定可比对。
var Columns = []string{"id", "name", "dept", "title", "phone", "id_card", "salary"}

// toRow 将员工记录展开为列名 -> 原始值的映射。
func toRow(e Employee) map[string]interface{} {
	return map[string]interface{}{
		"id":      e.ID,
		"name":    e.Name,
		"dept":    e.Dept,
		"title":   e.Title,
		"phone":   e.Phone,
		"id_card": e.IDCard,
		"salary":  e.Salary,
	}
}
