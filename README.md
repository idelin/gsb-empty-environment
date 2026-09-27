# tracecheck — 有界可见轨迹等价性比较

比较两个版本状态机的**可见行为**（可见轨迹集合），在有界长度内找出新增或消失的轨迹。
内部状态名变化不算行为变化；允许 tau（静默）迁移与非确定性。Python 3.11，仅标准库 + unittest。

## 方法

- 语义：可见轨迹 = 运行投影到非 tau 标签后得到的词；空机器（无初始态）连空轨迹都没有。
- 算法：对两台机器做**按需子集构造**（epsilon 闭包处理 tau 环），在子集对的**乘积空间上 BFS**。
  一侧可达子集为空而另一侧非空时，当前词即区分轨迹；BFS 保证返回**最短**区分可见序列。
  这是子集（幂集）层面的比较，不是内部状态一对一匹配。
- 反例：附带两边读完该词后的可达状态集合作为证据，`Counterexample.replay(m1, m2)` 可重放核验。
- 裁决：`equivalent_up_to_bound`（界内无差异，界外结论未知）、`different`（附最短反例）、
  `unknown`（步数预算耗尽，保持未决；未决结果不缓存）。
- 缓存：`BehaviorComparator` 以 `(版本号, 内容指纹)` 为键；版本更新或结构变化都会正确失效。
- 核验：`tracecheck/brute.py` 对有界词集做暴力枚举，测试中与乘积搜索结果交叉比对（60 组随机小模型）。

## 运行

```bash
python3.11 -m tracecheck.demo          # 运行样例
python3.11 -m unittest discover -s tests -t . -v   # 测试
```

## 样例输出

```
== v1 vs renamed v1 (pure renaming) ==
verdict: equivalent_up_to_bound (bound=6, explored=5 subset pairs)

== v1 vs v2 (v2 adds 'cancel') ==
verdict: different (bound=6, explored=4 subset pairs)
word [order cancel] is a visible trace of the second machine only
  first  reachable subset: []
  second reachable subset: ['s0']
  -> the first machine cannot produce this word

counterexample replays on both machines: True

== v1 vs v2 with tiny step budget (stays pending) ==
verdict: unknown (bound=6, explored=1 subset pairs)

== cache ==
after two identical compares: hits=1 misses=1
after updating v2 -> v3:          hits=1 misses=2 (stale entry not reused)
```

## 测试结果

```
Ran 18 tests in 0.003s
OK
```

覆盖：纯重命名等价、新增/消失轨迹、最短反例、tau 环与 tau 自环、空轨迹与空机器、
非确定性子集语义（非状态对匹配）、界外结论保持未知、预算耗尽保持未决且不缓存、
缓存命中与版本/结构更新失效、反例可重放与篡改检测、随机模型暴力枚举交叉核验。
