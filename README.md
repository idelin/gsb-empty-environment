# 栈历史检查器（stack_checker）

读取一段并发栈的**调用 / 返回**事件历史（push 写、top 读、pop 读改写），
判断它能否对应一个合法的顺序栈（可线性化）。操作数 ≤ 20，允许操作未返回
（pending）。若可线性化则输出一个线性化顺序；否则输出事件流的**最短矛盾前缀**。

## 构建与运行

```sh
make            # 或 g++ -std=c++17 -O2 -o stack_checker stack_checker.cpp
./stack_checker < examples/linearizable.txt
./stack_checker --ignore-gen < examples/aba_stale.txt   # 朴素模式演示
```

## 输入格式

每行一个事件，按真实时间先后排列；`#` 之后为注释。

```
call <id> push <addr>:<gen>   # 压栈（写）
call <id> pop                 # 弹栈（读改写）
call <id> top                 # 读栈顶（读）
ret  <id> ok                  # push 的返回
ret  <id> empty               # pop/top 返回空
ret  <id> <addr>:<gen>        # pop/top 返回的值
```

没有对应 `ret` 的操作视为 pending：它可能尚未生效（不参与线性化），
也可能已生效（以任意符合顺序栈语义的方式参与线性化）。

## 输出

- `LINEARIZABLE` + 一个合法线性化顺序（pending 操作若被使用会标注 `[pending]`）；
- `NOT LINEARIZABLE` + 最短矛盾前缀：前 k 个事件已无可行线性化，而前 k-1 个仍可。
  非线性化性对前缀单调（新增 call 只增加可选的 pending 操作，新增 ret 只增加约束），
  故用二分定位 k。

## 算法

回溯搜索线性化：状态为（已线性化操作掩码， 栈内容），按实时序约束剪枝
（操作 A 的返回早于 B 的调用 ⇒ A 必须先线性化），用哈希集合记忆已失败的
状态。另有一个 O(n) 必要条件预检：每个值被已返回 pop 弹出的次数不能超过
它被压入的次数。一般线性化判定是 NP 难的，本工具面向 ≤ 20 个操作的历史。

## 地址复用代次与 ABA 反例

值带 `(地址, 代次)` 标签：地址被回收复用时代次递增，以此区分"同一个地址的
不同化身"。`examples/aba_stale.txt` 中，地址 `a` 先以 `a:1` 压入并弹出，
随后以 `a:2` 再次压入；最后的 pop 却返回旧代次 `a:1`：

```
call 1 push a:1      call 3 push a:2
ret 1 ok             ret 3 ok
call 2 pop           call 4 pop
ret 2 a:1            ret 4 a:1      <- 栈顶是 a:2，返回了旧代次
```

正确检查器判 `NOT LINEARIZABLE`；而 `--ignore-gen`（只比对地址的朴素模式）
会把旧地址误认作新值，错误地接受该历史——这正是 ABA 问题中"旧地址误认"
的反例。
