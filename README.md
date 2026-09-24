# cacheaudit

有界缓存事件日志 + 线性一致性审计器。纯 Go 标准库，无远程依赖。

## 组成

- `event.go` — 事件（命中/失效/加载/淘汰/发布）与有界环形日志；覆盖最旧事件时累计
  `dropped`，`dropped>0` 即证据不完整。
- `cache.go` — 缓存。数据源（`Loader`）、时钟、失败、资源释放钩子全部可注入；
  未发布的加载不记为命中；命中率分母固定为 `hits+misses`。
- `audit.go` — 审计器。读取带调用-返回区间的事件历史，按公开一致性契约搜索合法
  线性化顺序（回溯 + 记忆化），并用实序重放定位确定陈旧的命中。
- `reference.go` — 独立的全排列参考核验实现，与主搜索不共享代码，用于小历史交叉验证。
- `minimize.go` — 把违反契约的历史自动缩减为 1-极小子集，重编号并保留来源版本证据。

## 公开一致性契约（顺序规范）

- `LOAD(v)`：暂存版本 v，对命中不可见；
- `PUBLISH`：要求存在暂存版本，提升为可见版本；
- `EVICT`：清空暂存与可见版本；
- `HIT(v)`：合法当且仅当 v 等于当前可见版本且非 0；
- `MISS`：合法当且仅当当前无可见版本；
- 实序约束：A 的返回不晚于 B 的调用（`A.End <= B.Start`）则 A 必须排在 B 之前。

## 判定口径

- `LINEARIZABLE`：证据完整且存在合法线性化顺序；
- `STALE`：证据完整且确定陈旧（附命中序号、实得/应得版本、来源加载序号）；
- `INCONCLUSIVE`：日志丢片、环形覆盖、序号缺口或来源加载缺失——报告证据不足，
  绝不猜测完整历史。

## 运行

```sh
go test ./...        # 全部测试
go run ./cmd/cacheaudit   # 演示：健康 / 确定陈旧 / 环形覆盖三场景
```

## 运行样例（`go run ./cmd/cacheaudit` 节选）

```
== scenario 1: healthy cache ==
stats: hits=2 misses=4 hitRate=0.33 (denominator=hits+misses=6)
  #5 [7,7] PUBLISH key="a" v=1 src=load:4
  #6 [8,9] HIT     key="a" v=1 src=load:4
audit: verdict=LINEARIZABLE
resource released: true

== scenario 2: stale return after newer publish ==
injected stale hit: #9 [100,101] HIT     key="a" v=1 src=load:2
audit: verdict=STALE reason="hit version contradicts a publish/evict ..."
  culprit: hit #9 key="a" got v1 want v2 (source load #2)
--- minimized stale subset (2 of 9 events) ---
  #1 [1,3] LOAD    key="a" v=1 src=loader
  #2 [100,101] HIT     key="a" v=1 src=load:1
audit(minimized): verdict=STALE

== scenario 3: ring overwrite loses evidence ==
log dropped 8 events (capacity 4)
audit: verdict=INCONCLUSIVE reason="log incomplete: 8 events dropped ..." dropped=8
```

## 测试结果（`go test -count=1 ./...`）

```
ok  	cacheaudit	1.162s
```

17 个测试全部通过，覆盖：有界日志与丢片计数、未发布不记命中、固定分母口径、
注入失败记为失效、资源恰好释放一次且关闭幂等、确定陈旧定位（含淘汰后命中、
可见时失效）、区间重叠可线性化、丢片/缺口/来源缺失判证据不足、
与全排列参考实现的 300 组随机交叉核验、最小化子集仍违反契约且 1-极小并保留来源证据。
