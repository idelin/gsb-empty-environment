// Package identity 的核心契约。
//
// 偏序与链分解
//   - 同一 Chain 内事件按输入切片顺序构成全序；After 给出额外跨线程屏障。
//   - 分析不假设日志写入顺序等于身份切换顺序，只在满足偏序的交错上推进。
//   - 输入事件数上限为 36，超出由 Analyze 在入口拒绝。
//
// 身份与句柄
//   - Token 绑定 Tenant 与 Generation；每线程维护活动身份栈与挂起栈。
//   - Borrow 压入普通借用帧并记录所属线程；Restore 只能弹出本线程栈顶借用帧，
//     跨线程归还与同线程嵌套（LIFO）异常都作为非法迁移审计。
//   - Spawn 只捕获当时的有效身份快照并写入队列槽；槽可复用，但句柄身份由
//     (Slot, HandleGeneration) 确定，旧代次恢复失效，句柄一次性消费。
//   - Exit 显式校验普通借用栈为空；借用未清的退出在任何交错中都非法。
//
// 合法历史与非法证据
//   - 非法迁移立即拒绝并记入 IllegalAttempts，绝不作为受保护读取的违规证据。
//   - 若剩余就绪事件全部是不可合法执行的非退出事件，当前最大合法前缀视为
//     完整合法历史；只有显式 Exit 失败才令 HasLegalHistory=false。
//
// 分类、预算与证明
//   - 每个受保护读取分类为 AllConform、AllViolate、Mixed、Unreachable，
//     预算截断或无完整合法历史时降级为 Unknown 并在 Reason 中说明。
//   - 两类可达事实（Conform/Violation）各自给出 BFS 最短合法前缀，可用
//     VerifyWitness 独立复放复核。
//   - 均匀结论附带与读取 ID 严格绑定的 CoverageCertificate；VerifyCoverage
//     会独立重跑分析并核对覆盖状态集合的完备性，绑定到其他读取、伪造状态或
//     篡改模型指纹的证书一律拒绝。
package identity
