# buildcache: 确定性多输出构建缓存

纯 Python 3.11 标准库实现，无网络访问。包与构建输入全部来自本地 JSON
（`data/packages.json`、`data/build_plan.json`），版本号为
`major.minor.patch` 并支持 `-prerelease` 预发布标记。

## 核心语义

- **全部输出通过摘要检查才发布**：节点的每个输出都对上 `sha256` 后才写入
  缓存条目；任一输出缺失或摘要不匹配，其余输出一律不发布。
- **失败清理**：构建在临时 staging 目录进行，失败时整体删除，不留残余文件。
- **并发去重**：同键并发构建只执行一次（in-flight 合并），其余线程等待并
  共享同一条目；同键同内容的重复构建直接命中缓存。
- **非确定性冲突**：同键不同产物在发布时抛 `ConflictError`，已缓存条目
  不被覆盖（后写不能覆盖前写）。

## 结构

- `buildcache/version.py` — 语义化版本解析与排序（含预发布）
- `buildcache/manifest.py` — 本地 JSON 清单加载、节点键计算
- `buildcache/cache.py` — 内容寻址缓存存储（去重 / 冲突检测）
- `buildcache/builder.py` — 构建执行、摘要校验、并发合并、失败清理
- `scripts/gen_plan.py` — 由 `packages.json` 重新生成 `build_plan.json`
- `demo.py` — 六个场景的端到端演示
- `tests/` — unittest 测试套件

## 运行

```sh
python3 scripts/gen_plan.py   # 重新生成构建计划（可选，已提交）
python3 demo.py               # 端到端演示
python3 -m unittest discover -s tests -v
```

## 运行样例（`python3 demo.py`）

```text
loaded 2 packages (core 1.2.3, plugin 0.9.0-rc.1), 3 nodes from .../data

[1] cold build: publish only when every output passes the digest check
    core-lib: published 2 outputs (lib/core.map, lib/core.o)
    core-doc: published 1 outputs (doc/core.html)
    plugin-ui: published 2 outputs (bin/ui.so, bin/ui.sym)

[2] rebuild: identical key+content is deduplicated (cache hit)
    3 nodes rebuilt -> 0 real build executions

[3] concurrent duplicate builds of the same key
    8 threads -> 1 real build, 1 distinct entry, 0 errors

[4] one output missing -> nothing is published
    rejected: node core-lib: missing output lib/core.map
    cache entry present: False; staging left behind: False

[5] tampered output -> digest mismatch, partial outputs cleaned up
    rejected: node core-lib: digest mismatch for lib/core.o: expected e65e60b8bcca..., got 2afd9429427b...
    cache entry present: False; staging left behind: False

[6] forced rebuild with different artifacts -> non-determinism conflict, first write wins
    conflict: non-deterministic build for key 8fa7d407f3d9...: cached digest e2c8ca585557... != new digest 128758d8019b...
    cached digest unchanged: True
```

## 测试结果（`python3 -m unittest discover -s tests -v`）

```text
test_all_outputs_verified_before_publish ... ok
test_concurrent_duplicate_builds_run_once ... ok
test_digest_mismatch_cleans_up ... ok
test_missing_output_publishes_nothing ... ok
test_nondeterministic_conflict_keeps_first_entry ... ok
test_rebuild_is_cache_hit ... ok
test_conflict_writes_no_objects ... ok
test_entries_survive_reopen ... ok
test_get_missing_key_returns_none ... ok
test_publish_and_get_roundtrip ... ok
test_same_key_different_content_conflicts ... ok
test_same_key_same_content_dedupes ... ok
test_bad_output_digest_rejected ... ok
test_duplicate_package_rejected ... ok
test_invalid_package_version_rejected ... ok
test_key_changes_with_inputs ... ok
test_load_packages_with_prerelease ... ok
test_load_plan_and_stable_key ... ok
test_node_without_outputs_rejected ... ok
test_unknown_package_rejected ... ok
test_equality_and_hash ... ok
test_numeric_beats_alpha_identifiers ... ok
test_release_beats_prerelease ... ok
test_semver_org_chain ... ok
test_invalid_versions_rejected ... ok
test_non_string_rejected ... ok
test_plain_version ... ok
test_prerelease_version ... ok

----------------------------------------------------------------------
Ran 28 tests in 0.190s

OK
```
