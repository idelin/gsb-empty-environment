// Package netsec 提供认证 begin/end 事件的注入匹配验证。
//
// 背景：协议中的同一个 begin（开始/注入）事件可能被多个 end
// （结束/消费）事件重复消费，从而形成重复认证。本包把问题建模为
// begin 与 end 之间的二分图匹配：一条边存在当且仅当两端的主体、
// 会话随机数、操作摘要完全一致，且 begin 在事件流中早于 end。
//
// 每个 end 必须匹配一个更早且未被其他 end 使用的 begin；允许并发
// 会话（不同会话随机数互不影响）与部分未结束的 begin。Analyze
// 基于增广路算法判定是否存在覆盖全部 end 的合法匹配，失败时返回
// Hall 型缺口证据；EnumerateMatchings 枚举所有完整匹配；CrossCheck
// 用多条独立路径互相核对结论。
package netsec
