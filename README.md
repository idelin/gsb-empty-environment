# WAF规则调用会产生可信或不可信的净化标记，普通布尔化简可能把未执行的净化当成已完成

WAF规则调用会产生可信或不可信的净化标记，普通布尔化简可能把未执行的净化当成已完成。写供Go规则发布层调用的编译包，叶子检查返回真假并产生限定字段的标记，危险转发节点要求指定标记已存在；短路、异常和拒绝均有明确控制边。优化可共享纯检查，但不得共享有标记副作用的执行。返回逐路径标记保证及最短缺失见证，至多12个布尔输入，用原解释器与编译结果全枚举比较实际检查次序、标记和是否转发。

## 实现

包 `taskpkg`（module `example.org/domainreissue/q008`）提供：

- `Rule`/`Expr`/`Stmt`/`Check`/`Forward`：规则 IR。叶子 `Check` 返回布尔值，
  可按 `Emit` 模式产生字段级 `Mark`（`Trusted` 区分可信/不可信），也可在指定
  输入上抛异常；`Forward` 要求一组标记已存在，可信需求只能由可信标记满足。
- `Compile`：把校验通过的规则 lower 成显式边 CFG `Program`。短路
  （`and.false`/`or.true`）、异常（`leaf.raise`/`try.catch`/`raise.escape`）、
  拒绝（`reject`）和转发（`forward.allow`/`forward.deny`）都是显式控制边。
- `Interpret` 与 `Program.Evaluate`：参考解释器每次出现点都执行；编译结果只对
  无标记、不抛异常的纯检查做 memo 共享，有副作用的出现点一律重新执行。两者跑同一
  CFG 内核，`CompareTraces` 逐边、逐叶子出现点、逐标记、逐决策比较。
- `Program.Enumerate`：在全部 `2^n` 赋值上聚合每个转发节点的逐路径标记保证
  （可达标记集、恒存在标记、允许/拒绝集合）。
- `Program.ShortestMissingWitness`：在 `(pc, 已固定输入, 标记集)` 上做 BFS，
  返回使指定转发节点因缺少某标记而拒绝的最短具体赋值见证。

输入数上限 `MaxInputs = 12`。

## 验证

- `go test ./...`：行为、校验失败、全枚举差分（手工规则 + 40 条随机规则）、
  12 输入 4096 赋值全枚举、BFS 见证与暴力法交叉校验。
- `go test -race -cover ./...`：竞态检查，语句覆盖率约 88%。
