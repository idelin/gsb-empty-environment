// Package netsec 提供安全回归反例的跨版本持久身份与关联能力。
//
// 反例 [Counterexample] 由三部分构成：跨版本稳定的断言 ID、记录对象依赖链
// 的对象来源图 [Graph]，以及用于递归结构相等比较的结构化输入 [Input]。
//
// 跨版本关联 [Associate] 只接受调用方显式提供的节点迁移映射
// [NodeMigration]。映射必须覆盖旧图全部节点并保持每条依赖边，违例通过
// [Violation] 暴露；一个节点存在多个迁移候选时保留全部候选并给出
// [VerdictAmbiguous] 结论。本包不包含任何基于路径、名称或错误文本相似度
// 的自动合并：断言 ID 不同或输入结构不同的两个相近缺陷会被判为
// [VerdictDiverged]。
package netsec
