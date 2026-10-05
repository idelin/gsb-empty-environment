package main

// Group 是一个不可分割的用户组：同组用户必须在同一版本上。
type Group struct {
	Name    string `json:"name"`
	Users   int64  `json:"users"`
	Version string `json:"version"` // 当前所在版本
}

// Input 是输入文件的结构。
type Input struct {
	Groups []Group `json:"groups"`
}

// Solution 表示一个可行分配方案。
type Solution struct {
	Mask        uint64 // 第 i 位为 1 表示第 i 个组被分配到目标版本
	NewUsers    int64  // 目标版本上的总用户数
	MovedUsers  int64  // 迁移代价：版本发生变化的用户总数
	MovedGroups int    // 发生迁移的组数
}
