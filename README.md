# 多输出构建缓存

Python 3.11、仅标准库实现的确定性构建缓存：

- **全量通过才发布**：节点声明的每个输出都必须在暂存目录中生成；任一缺失即构建失败，不发布任何输出。
- **摘要检查**：每个缓存条目带 `manifest.json`，记录各输出的 sha256；读取时重新哈希校验，缺失/被篡改视为未命中。
- **原子发布**：先写暂存目录再 `rename`，发布失败自动清理，不会出现半成品条目。
- **并发重复构建**：同键并发构建走 single-flight，只执行一次 action，其余线程共享结果。
- **同键同内容去重**：再次发布（含强制重跑）摘要相同则去重，不写新条目。
- **非确定性冲突**：同键不同内容抛 `NonDeterministicConflict` 并记录冲突，**后写不覆盖前写**。
- **失败清理**：action 抛异常或缺输出时，暂存目录被删除，缓存保持原状。
- **本地 JSON 包清单**：包与构建输入只来自本地 JSON；版本为 `major.minor.patch`，可带 `-prerelease`（预发布序低于正式版）；不联网查包。

## 目录结构

```
buildcache/
  version.py    # 版本解析与排序
  manifest.py   # 本地 JSON 包注册表（离线）
  cache.py      # 原子发布、摘要校验、去重与冲突
  builder.py    # single-flight 并发执行、缺输出判定、失败清理
examples/
  demo.py       # 端到端运行样例
  packages.json # 示例本地包清单
tests/          # unittest 测试
```

## 运行测试

```bash
python3.11 -m unittest discover -s tests -v
```

## 运行样例

```bash
python3.11 examples/demo.py
```
