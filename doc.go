// Package netsec 提供供 Go 验证宿主调用的计时调度器与认证小模型，
// 用于检查“超时动作被零耗时消息无限推迟”的公平性缺陷。
//
// 核心设计把两类动作严格分开：
//   - 时间推进只能通过 Scheduler.Advance 或 Scheduler.AdvanceToNext 显式发生；
//   - Scheduler.Post / Scheduler.PostAt 产生的零耗时普通消息不消耗任何时间。
//
// 当虚拟时钟到达某截止点时，TimeoutEvent 在同一时刻总是先于 NormalEvent
// 派发，保证本应拒绝的会话不会被同刻的普通消息挽留。
//
// 为阻止无限零耗时循环，调度器对相邻两次成功时间推进之间的零耗时步数
// 设有显式上限 MaxZeroSteps：触顶即返回 ErrLivelock。SearchZeroCycle
// 则在冻结时钟下基于状态指纹搜索循环并返回可复现的 CycleEvidence。
package netsec
