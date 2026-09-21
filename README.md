# 多输出本地构建缓存（Python 3.11，仅标准库）

一个离线、线程安全的多输出动作缓存原型：多输出节点只有在**全部输出**
通过摘要检查后才一次性发布缓存条目；支持并发重复构建的单飞去重、失败
清理；同键不同产物判定为非确定性冲突，先写者胜出、后写永不覆盖。

## 规则映射

| 需求 | 实现位置 |
| --- | --- |
| 全部输出通过摘要检查后才发布 | `buildcache/engine.py` `_build_and_publish`：先 `StagingArea.verify` 全量校验，再写 blob 和条目 |
| 一个输出缺失不得发布其余输出 | `buildcache/store.py` `StagingArea.verify`：缺文件抛 `MissingOutputError`，在此之前不写任何 blob/条目 |
| 摘要不匹配 / 空文件 | 抛 `DigestMismatchError`，不发布，暂存目录无条件清理 |
| 并发同键同内容去重 | `ActionCache` 按 key 单飞（single-flight），并发请求共享同一个 `attempt_id`，只构建一次 |
| 同键不同内容冲突 | 条目 create-once（`os.O_EXCL` + 内容比对），抛 `NonDeterministicConflictError`，保留首次结果 |
| 失败清理 | 暂存目录放在 `root/tmp/`，`finally` 中 `shutil.rmtree` |
| 输入来自本地 JSON | `buildcache/manifest.py` 解析 `examples/manifest.json`，不联网 |
| 版本 major.minor.patch + 预发布 | `buildcache/version.py`，按 SemVer 2.0 比较预发布标识 |
| 不联网查包 | 包名/版本仅来自本地清单，代码中没有任何网络调用 |

## 目录结构

```
buildcache/
  version.py    # 版本号解析与预发布排序
  manifest.py   # 本地 JSON -> Package/BuildNode/BuildOutput
  store.py      # 内容寻址存储(CAS)、暂存区、条目 create-once 发布
  engine.py     # ActionCache 单飞调度、全量校验发布；SimulatorBuilder
  errors.py     # 领域异常
examples/
  manifest.json # 本地包与构建输入（含二进制 base64 输出、预发布版本）
  run_demo.py   # 离线运行样例
tests/
  test_version_manifest.py
  test_store_engine.py
```

## 磁盘布局

```
<cache-root>/
  blobs/ab/<sha256>...      不可变内容，按摘要寻址，相同内容天然去重
  entries/<safe-key>.json   已发布条目（输出路径->摘要、聚合摘要、attempt_id）
  tmp/<key>-<uuid>/         每次构建的私有暂存目录，结束即清理
```

条目示例：

```json
{
  "key": "//core:release",
  "package": "core-lib",
  "version": "2.4.1-rc.1",
  "attempt_id": "edd42e11…",
  "outputs": {
    "config/version.conf": "8a5f6ac0…",
    "lib/core.dat": "630dcd29…"
  },
  "output_digest": "…(全部输出 path:digest 的聚合 sha256)",
  "built_at": "2026-09-21T…+00:00"
}
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v   # 测试
python3.11 examples/run_demo.py              # 运行样例
```

## 发布时序（关键不变量）

1. 同一 key 的并发请求在 `_flights` 上汇合，只有一个 leader 真正构建；
   leader 失败时等待者会重新竞争并重试自己的构建。
2. builder 只写私有暂存目录，不接触共享缓存。
3. 逐输出校验：必须存在、非空、（若声明了 sha256）重算摘要一致。
   任一输出失败即整体放弃，清理暂存目录。
4. 全部通过后才把输出写入 CAS（相同内容只存一份），随后 create-once
   发布条目；已有同内容条目返回去重，不同内容报非确定性冲突且不覆盖。

声明了输出 sha256 的节点会先走快速命中路径：现有条目与声明摘要全部一致
时直接返回 `cached`，无需重新构建。

## 运行样例摘要

- `//core:release` 首次构建发布 2 个输出；再次构建 `deduped`，同一 `attempt_id`。
- 5 个并发相同构建只产生 1 个 `attempt_id`（单飞去重）。
- `//tools:missing-output` 缺 1 个输出 -> 无条目、无 blob、暂存目录清理。
- `//tools:bad-digest` 声明摘要不符 -> `DigestMismatchError`，无条目。
- `//flaky:image` 同键两次不同内容 -> `NonDeterministicConflictError`，首次结果保留。
- 不同 key 并发互不阻塞；声明摘要的节点返回 `cached`。
