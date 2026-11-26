package memo

import "fmt"

func errUnknownKind(kind string) error {
	return fmt.Errorf("memo: unknown logical node kind %q", kind)
}

func errUnknownGroup(id int) error {
	return fmt.Errorf("memo: unknown group %d", id)
}

// ErrEnforcerCycle 在物理搜索中检测到 Enforcer 自循环时返回。
// 正常实现下该错误不应出现；它是防止“Enforcer 套 Enforcer”无限递归的最后防线。
type ErrEnforcerCycle struct {
	group int
	req   PhysProps
}

func (e *ErrEnforcerCycle) Error() string {
	return fmt.Sprintf("memo: enforcer cycle detected at group %d (%s)", e.group, e.req.key())
}
