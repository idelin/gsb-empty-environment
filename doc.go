// Package netsec 提供控制流图上的授权保证分析：判断每个敏感调用是否在
// 所有到达路径上都存在“主体、对象、版本”三者匹配的授权检查。
//
// # 分析模型
//
// 宿主把程序翻译成 [Graph]：节点类型见 [NodeKind]，边分为普通边与异常边
// （见 [EdgeKind]）。抽象状态在每个程序点记录：
//   - 当前各对象的版本（初始版本为 [InitialVersion]，修改节点产生由
//     [ModifiedVersion] 标识的新版本；合流时版本不一致则退化为
//     [UnknownVersion]）；
//   - 所有到达路径共同持有的 [Credential]（must-凭据集合）。
//
// 凭据是（主体, 对象, 版本）三元组，只能证明授权检查发生时的原主体、
// 原对象与原版本：
//   - [KindAuthCheck] 按对象当前版本加入一条凭据；
//   - [KindModifyVersion] 提升对象版本，旧版本凭据保留但与新版本不再匹配；
//   - [KindTransferCredential] 只原样传递凭据，不改变其主体、对象、版本，
//     因此不能把授权扩大到新主体；
//   - [KindSensitiveCall] 在入口状态中查找完全匹配的凭据，缺失即报警。
//
// 合流取所有前驱状态的交集，即只保留所有路径共同保证的事实。分析在循环
// 上做单调混沌迭代，状态格有限，必在有限步内到达不动点（迭代次数见
// [Result.Iterations]）。每个报警附带一条边数最少的违反路径
// [Finding.Path]，异常边会在 [PathStep.Via] 中如实标注。
package netsec
