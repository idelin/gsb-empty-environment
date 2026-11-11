// Package netsec 实现邮件策略服务使用的三值认证决策。
//
// 原子事实取值为 Pass、Fail 或 Unknown：认证结果缺失、无法判定或未提供都
// 属于 Unknown，绝不与 Fail 混淆。规则由 AND、OR、NOT 构成，按强 Kleene
// 逻辑求值：
//
//   - NOT：Unknown 的否定仍是 Unknown；
//   - AND：任一子式为 Fail 即 Fail，否则任一为 Unknown 即 Unknown，否则 Pass；
//   - OR：任一子式为 Pass 即 Pass，否则任一为 Unknown 即 Unknown，否则 Fail。
//
// 规则经 Compiler 编译为有向无环图：结构相同的子式自动归并为同一节点，
// 可被多个父节点共享；求值时按节点记忆化，共享子式只计算一次。
//
// 最终规则值 Fail 对应拒收（Reject），Unknown 对应待定（Pending），
// Pass 对应放行（Allow）。
//
// Explain 在至多 MaxFacts（20）个事实上枚举维持当前结论所需的最少证据：
// 未选事实一律视为 Unknown；同数量候选按事实 ID 字典序取最小集合。
package netsec
