# Biscuit衰减块事实隔离

Biscuit 令牌允许持有者追加衰减块，若后加的块能给自己添加 authority 事实，持有者就能自行提权。为Go授权服务写可导入的 Biscuit 块信任评估包，只处理本题约定的 Datalog 子集（事实 pred(常量…)、规则 head :- body、check if、allow if、deny if，变量以 $ 开头）：块 0 为 authority 块，其余为衰减块；authority 块的事实与规则对所有块可见，衰减块自己推出的事实只对本块的 check 可见，不进入 allow 与 deny 策略所用的事实集合；衰减块里 head 谓词与 authority 块同名的规则，其推出的事实不得写入 authority 作用域；所有块的 check 都必须通过，再依次评估服务端 allow 与 deny 策略，第一条匹配者决定，无匹配则拒绝；规则迭代到不动点，最多 100 轮，超限拒绝。用衰减块伪造 right 事实、check 失败、deny 先匹配、不动点超限和变量绑定验证。
