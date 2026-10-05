# 栈历史检查器 (stack_checker)

读取一段并发栈的历史（调用 / 返回事件），判断它能否对应某个合法的
顺序栈执行（可线性化性检查）。操作数 ≤ 20，允许操作未返回（pending）。

## 构建

```sh
g++ -std=c++17 -O2 -Wall -o stack_checker stack_checker.cpp
```

## 输入格式

每行一个事件，`#` 之后为注释。值形如 `<addr>:<gen>`（地址:复用代次）。

```
call <id> push <addr>:<gen>    # 写操作
call <id> pop                  # 读改写操作
call <id> peek                 # 读操作（不弹出）
ret  <id> ok                   # push 的返回
ret  <id> empty                # pop/peek 返回空栈
ret  <id> <addr>:<gen>         # pop/peek 返回栈顶值
```

## 输出

- 可线性化：输出 `LINEARIZABLE` 及一个合法线性化顺序；pending 操作
  追加在末尾并给出假定结果（pending 操作可丢弃或任意补全，不影响判定）。
- 不可线性化：输出 `NOT LINEARIZABLE` 及**最短矛盾前缀**——使历史
  不可线性化的最短事件前缀，并指出无法匹配的返回事件。
  （由可线性化性的局部性，前缀矛盾则整体必矛盾。）

## 算法

DFS 搜索状态 = (已消费事件数, 已线性化操作位掩码, 当前栈内容)，
配合失败状态记忆化。转移：消费下一个事件（ret 要求对应操作已线性化），
或线性化一个已调用且未线性化的操作（结果须与返回记录一致）。
实时间序（ret A 先于 call B ⇒ A 在 B 前）由事件消费顺序自然保证。

## 地址复用代次与 ABA 反例

值按 `(addr, gen)` 二元组比较：地址被分配器复用时代次递增，
防止把旧单元误认作新单元。`--demo` 运行内置反例：地址 7 以代次 0
入栈并弹出后被复用为代次 1，而某个 pop 却返回了旧代次 `7:0`——

- 代次感知检查：**拒绝**（最短矛盾前缀 = 8 个事件）；
- `--ignore-gen` 朴素检查（只看地址）：**错误接受**，把旧单元 `7:0`
  误认作当前单元 `7:1`。

## 用法

```sh
./stack_checker examples/linearizable.txt   # 合法历史 -> 线性化顺序
./stack_checker examples/pending.txt        # 含未返回操作
./stack_checker examples/contradiction.txt  # 矛盾 -> 最短矛盾前缀
./stack_checker examples/aba.txt            # ABA 反例（拒绝）
./stack_checker --ignore-gen examples/aba.txt  # 朴素模式（错误接受）
./stack_checker --demo                      # 内置 ABA 对照演示
cat history.txt | ./stack_checker           # 从 stdin 读取
```

退出码：0 = 可线性化，1 = 不可线性化，2 = 输入错误。
