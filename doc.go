// Package netsec 实现特权启动器在 execve(2) 时的 Linux 能力集合转换，
// 供 Go 安全宿主在执行文件前收窄进程能力。
//
// 转换规则对应 capabilities(7)：
//
//	P' = (I ∩ F_i) ∪ (F_p ∩ B) ∪ A'
//
// 其中特权文件（携带文件能力或置了 set-user-ID/set-group-ID 位）会先清空
//
//	ambient 集合（A' = ∅），普通文件保留 ambient。新 effective 集合按文件
//
// effective 标记在新 permitted 集合与 ambient 集合之间选择。
//
// no_new_privs 禁止提权：文件能力与 setuid/setgid 位全部忽略，且要求
// 新 permitted 集合不超出旧 permitted 集合。文件能力在当前用户命名空间
// 无映射时，按无文件能力处理。
package netsec
