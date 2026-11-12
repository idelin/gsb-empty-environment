// Package netsec 为 Go 沙箱管理组件提供 Linux Yama ptrace_scope 的授权
// 判定模拟，供沙箱在不真正发起 ptrace 系统调用的情况下预判一次
// PTRACE_ATTACH / PTRACE_TRACEME 是否会被内核允许。
//
// 核心组件：
//
//   - ProcessTree 模拟随 fork 与 exit 变化的进程树，进程退出后其孤儿
//     子进程被重新挂载到 init，不再属于原祖先的后代；
//   - CapSet / CAP_SYS_PTRACE 模拟进程能力；
//   - Authorizer 按 scope 0..3 与 PR_SET_PTRACER 授权做出 Decision。
//
// 判定规则（与内核 security/yama/yama_lsm.c 对齐）：
//
//   - 先做基本检查：调用者与目标同 uid，或调用者持有 CAP_SYS_PTRACE；
//   - scope 0：通过基本检查即可；
//   - scope 1：目标须为调用者后代，或目标用 PR_SET_PTRACER 指定了
//     该调用者 / PR_SET_PTRACER_ANY；CAP_SYS_PTRACE 可绕过；
//   - scope 2：仅 CAP_SYS_PTRACE 持有者可 ptrace；
//   - scope 3：一律禁止，能力也无法绕过；
//   - PTRACE_TRACEME 不受 scope 1 的关系限制。
package netsec
