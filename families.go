package netsec

// DefaultFamilyTable 是默认的同族系统调用表。
//
// 攻击者只需改用同一族中的另一个系统调用，即可绕过只钩住单个调用的策略：
// 例如只钩 open 时改用 openat2，或用 creat/truncate 完成写访问。
var DefaultFamilyTable = FamilyTable{
	AccessOpen: {
		Access: AccessOpen,
		Name:   "open-family",
		Calls: []string{
			"open",
			"openat",
			"openat2",
			"creat",
		},
	},
	AccessRename: {
		Access: AccessRename,
		Name:   "rename-family",
		Calls: []string{
			"rename",
			"renameat",
			"renameat2",
		},
	},
	AccessTruncate: {
		Access: AccessTruncate,
		Name:   "truncate-family",
		Calls: []string{
			"truncate",
			"ftruncate",
		},
	},
}

// lookupFamily 按访问方式取出同族调用，未知访问方式返回 ok=false。
func (t FamilyTable) lookupFamily(access AccessMode) (SyscallFamily, bool) {
	family, ok := t[access]
	return family, ok
}
