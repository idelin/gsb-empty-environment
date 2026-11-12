package netsec

// Action 是钩子命中选择器后执行的动作。
type Action string

const (
	// ActionSigkill 终止触发钩子的进程。
	ActionSigkill Action = "Sigkill"
	// ActionOverride 篡改系统调用返回值，不实际执行该调用。
	ActionOverride Action = "Override"
)

// ParamType 是钩子参数的类型。
type ParamType string

const (
	ParamString ParamType = "string"
	ParamInt    ParamType = "int"
	ParamFlags  ParamType = "flags"
)

// Param 描述钩子的一个内核函数参数。
type Param struct {
	Name string
	Type ParamType
}

// Selector 是挂在某个参数上的匹配条件，Prefix/Equal/Postfix 之间为 AND 关系。
// 多个 Selector 之间为 OR 关系，任一命中即触发钩子动作。
type Selector struct {
	Param   string
	Prefix  string
	Equal   string
	Postfix string
}

// Hook 是策略中的一个内核函数挂钩。
type Hook struct {
	Call       string
	Params     []Param
	Selectors  []Selector
	Action     Action
	ReturnCode *int64 // 仅 Action 为 Override 时使用，nil 表示未填写
}

// Policy 是待保存的跟踪策略。
type Policy struct {
	Name  string
	Hooks []Hook
}

// AccessMode 是保护目标要求的访问方式，用于定位同族调用表。
type AccessMode string

const (
	AccessOpen     AccessMode = "open"
	AccessRename   AccessMode = "rename"
	AccessTruncate AccessMode = "truncate"
)

// Target 是组织要求保护的目标。
type Target struct {
	Path   string
	IsDir  bool
	Access AccessMode
}

// SyscallFamily 描述可达成同一种访问的一组系统调用。
type SyscallFamily struct {
	Access AccessMode
	Name   string
	Calls  []string
}

// FamilyTable 按访问方式索引同族调用表。
type FamilyTable map[AccessMode]SyscallFamily
