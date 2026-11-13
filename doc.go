// Package netsec 实现一个只支持约定 Datalog 子集的 Biscuit 块信任评估器。
//
// 支持的语法：
//   - 事实：pred(常量…)
//   - 规则：head :- body
//   - 块校验：check if body
//   - 服务端策略：allow if body / deny if body
//   - 变量以 $ 开头；常量支持裸标识符、双引号字符串与整数
//
// 信任模型：
//   - 块 0 是 authority 块，其余为衰减块；
//   - authority 块的事实与规则推导结果对所有块可见；
//   - 衰减块自行声明或推导的事实只在本块私有作用域内可见，仅服务本块 check，
//     既不会写回 authority 作用域，也不进入 allow/deny 策略的事实集合；
//   - 衰减块中与 authority 同名谓词的规则，其推导事实因此无法提权；
//   - 所有块的 check 必须全部通过，再按顺序评估服务端策略，第一条匹配者决定，
//     没有任何策略匹配时默认拒绝；
//   - 规则在每个作用域内迭代到不动点，最多 MaxRounds 轮，超限即拒绝。
package netsec
