package rootslot

// Validator 独立检查一个候选根的可达闭包：根页及从根可达的所有页
// 必须完整可读。Commit 在发布槽之前调用它；验证失败则槽绝不发布。
// 实现方（页管理器/B+树等）自行决定如何遍历闭包。
type Validator interface {
	ValidateReachable(root []byte) error
}

// ValidatorFunc 让普通函数充当 Validator。
type ValidatorFunc func(root []byte) error

func (f ValidatorFunc) ValidateReachable(root []byte) error { return f(root) }
