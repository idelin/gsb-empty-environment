# 多输出节点构建缓存（Multi-Output Build Cache）

Python 3.11 + 标准库 + `unittest` 实现的内容寻址构建缓存，**完全离线**：
包与构建输入只来自本地 JSON，不联网查询任何包。

## 特性

- **多输出节点的全有或全无发布**：任一输出缺失则拒绝发布，其余输出也不落盘；
  发布前对每个输出做 SHA-256 摘要检查，提交前再次校验（防暂存区被篡改），
  提交后回读校验，全部通过才视为发布。
- **并发重复构建去重**：
  - 缓存层：同键同内容 → `deduplicated`，不写任何文件；
  - 协调层（single-flight）：同键的并发构建只执行一次，其余调用者等待并复用结果。
- **非确定性冲突**：同键不同产物抛 `NonDeterministicConflict`，
  先写者胜出，后写绝不覆盖前写，冲突按输出逐项给出摘要差异。
- **失败清理**：构建失败 / 缺输出 / 摘要不一致时，私有暂存区与半成品条目目录
  一律清除，`tmp/` 不留残余；失败后可重试。
- **版本**：`major.minor.patch`（兼容 `1`、`1.2`），可标预发布
  （`3.0.0-rc.1` 或清单字段 `"prerelease": true/"rc.1"`），
  预发布版本按 SemVer 规则排序且低于正式版。
- 条目目录通过原子 `mkdir` 抢占，进程内用每键锁，跨进程也安全。

## 目录结构

```
buildcache/
  version.py      # major.minor.patch + 预发布解析/比较
  digest.py       # 规范化 JSON、SHA-256、缓存键派生
  manifest.py     # 本地 JSON 清单（包、节点、输入、输出）
  cache.py        # 暂存→摘要→原子发布→冲突/去重→失败清理
  coordinator.py  # single-flight 并发去重
tests/            # unittest，28 个用例
demo_manifest.json
run_example.py    # 端到端运行样例
example_output.txt
test_results.txt
```

## 缓存键

```
sha256(canonical_json({package, version, inputs(规范化), outputs(排序)}))
```

节点名不参与键：包/版本/输入/输出集合相同即同一个构建。

## 磁盘布局

```
<root>/entries/<key>/entry.json     # 清单（每输出 sha256/size、时间戳）
<root>/entries/<key>/<output...>    # 产物（原子 rename 落位）
<root>/tmp/.staging-<…>-<uuid>/     # 每次发布的私有暂存区（结束即删）
<root>/tmp/.build-<…>-<tid>/        # 协调器构建工作区（结束即删）
```

## 运行

```sh
python3.11 run_example.py                        # 运行样例
python3.11 -m unittest discover -s tests -v     # 测试
```

`example_output.txt` 与 `test_results.txt` 已保存一次实际运行输出。

## 发布协议（单个缓存键）

1. 检查声明的输出是否齐全（缺失 → `MissingOutputError`，不发布任何内容）。
2. 复制全部输出到私有暂存区并计算 SHA-256。
3. 加每键锁；键已存在则比较每个输出摘要：
   全部相同 → 去重；任一不同 → 非确定性冲突（保留既有条目）。
4. 提交前复算暂存摘要；原子 `mkdir` 抢占条目目录；
   `os.replace` 逐个落位，再原子写 `entry.json`。
5. 落盘后回读复核全部摘要；任一步失败则删除半成品条目与暂存区。
