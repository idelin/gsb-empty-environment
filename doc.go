// Package netsec 为 Go 容器运行时提供新挂载接口（open_tree / fsmount /
// mount_setattr / move_mount / close）的调用序列安全校验。
//
// 校验策略覆盖：
//   - 拒绝在调用方给出的敏感路径前缀下执行 OPEN_TREE_CLONE 克隆；
//   - move_mount 的来源描述符必须由本序列内的 open_tree 克隆或 fsmount
//     产生，且只能使用一次；
//   - 目标路径在检查时与使用时解析到的 inode 不一致（符号链接替换等
//     TOCTOU）即拒绝；
//   - 递归克隆带入的 shared 传播子树，必须先经递归 mount_setattr 改为
//     private 或 slave 才允许挂载；
//   - 描述符关闭后再使用一律拒绝。
//
// 使用无状态入口 [ValidateSequence] 一次性校验完整序列，或创建
// [Validator] 通过 [Validator.Feed] 增量喂入审计事件。
package netsec
