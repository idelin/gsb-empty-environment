# GOOSE状态号与序号准入

IEC 61850 GOOSE 用状态号 stNum 与序号 sqNum 表示事件与重发，攻击者重放旧帧，或伪造较大的 stNum 让订阅端接受假状态变化。为Go保护装置写可导入的 GOOSE 准入包，供二层订阅回调调用：EtherType 0x88B8，按（源 MAC、gocbRef、datSet、goID）定位发布者，未登记的组合丢弃；stNum 大于已知值视为新事件，要求 sqNum 为 0，否则丢弃；stNum 相等时 sqNum 须大于上一个，相等视为重复忽略，小于视为重放丢弃，回绕到 0 只在 sqNum 达到 2^32−1 后接受；stNum 变小一律丢弃，除非调用方给出该发布者的重启通告；timeAllowedToLive 到期未见新帧则把该发布者的数据标为失效；simulation 位为真的帧只被配置为测试模式的订阅端接受。用 stNum 跳变不置零 sqNum、重放、回绕、过期失效和 simulation 位验证。
