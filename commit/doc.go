// Package commit 实现镜像块设备的原地事务提交器与报价接口。
//
// 典型用法：
//
//	plan, rec, err := commit.Commit(inst, device)
//
// 其中 inst 描述扇区柱面、读费用函数、缓存槽数 K 与每个目标扇区的拼接配方；
// device 实现 Device 接口。规划（BuildPlan/Quote）只读取 inst，绝不接触盘面；
// Commit 才通过 Recorder 逐拍执行读、释放、写，并核对磁头、槽位、读成本、
// 组装来源与最终完整盘。无解时返回 *InfeasibleError。
//
// 优化目标是严格最小化总读费用（非负整数、int64 不截断累加），
// 同费用下最小化读次数。Enumerate 是独立的小图枚举核对器，仅供测试使用。
package commit
