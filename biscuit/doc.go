// Package biscuit 实现了本题约定的 Biscuit 块信任评估子集。
//
// # 支持的 Datalog 子集
//
// 文本语法（# 开头为行注释，语句可用换行或分号分隔）：
//
//   - 事实：pred(常量…)，例如 right("alice", "read")、revoked($id) 不允许出现在事实中
//   - 规则：head(…) :- body(…), body(…)，变量以 $ 开头，例如
//     grandparent($g, $c) :- parent($g, $p), parent($p, $c)
//   - 块约束：check if query(…) [|| query(…)]，多个查询为或关系
//   - 服务端策略：allow if query(…) 与 deny if query(…)，同样支持 ||
//   - 恒真查询：allow if true
//
// 常量支持标识符（admin）、双引号字符串（"alice"）、64 位整数（-7）、
// 布尔（true / false）。规则必须安全：head 中出现的每个变量都必须出现在 body 中。
//
// # 信任与作用域模型
//
// blocks[0] 是 authority 块，其余为衰减块：
//
//   - authority 块的事实与规则推出的事实构成 authority 作用域，对所有块只读可见；
//   - 每个衰减块在自己的私有作用域内求值，其事实与规则推出的事实只对本块 check 可见；
//   - 衰减块里 head 谓词与 authority 同名的规则，新事实只写入私有作用域，
//     永远不会污染 authority 作用域，因此持有者无法自行追加 authority 事实提权；
//   - 服务端 allow / deny 策略只在 authority 作用域上求值。
//
// # 评估流程
//
// 规则前向链迭代到不动点，单个作用域最多 MaxFixpointRounds（100）轮，
// 超限即拒绝。所有块的 check 全部通过后，按切片顺序评估服务端策略，
// 第一条命中的 allow / deny 决定结论；没有任何策略命中时默认拒绝。
package biscuit
