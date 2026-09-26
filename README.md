# 小型 CPU 张量：安全流式保存 / 恢复

Python 3.11，仅用标准库（`struct`、`hashlib`、`io`、`dataclasses`、`unittest`），
不依赖任何张量库或自动微分。包含自实现的步长视图张量、带校验的二进制编解码器，
以及逐字段变异、随机模糊、独立地址枚举器和独立数值验证。

## 文件

- `tensor.py` — `DType`（10 种数值类型）、`Storage`（共享字节缓冲）、
  `Tensor`（步长视图：形状/步长/偏移、切片产生同存储视图、负步长）、
  `validate_layout` / `reachable_byte_span`（偏移对齐、元素数溢出、可达地址范围）。
- `codec.py` — 二进制流式格式与“先校验、后原子发布”的加载器。
- `demo.py` — 运行样例：共享存储多视图往返 + 各类恶意输入拒绝。
- `test_tensor_codec.py` — 40 个 `unittest` 用例。

## 线格式（小端）

```
magic b"PMT1" | version u8 | entry_count u32
storage 条目: kind=1 | id u32 | nbytes u64 | payload[nbytes] | sha256(payload)
tensor  条目: kind=2 | id u32 | ref_id u32 | ref_kind u8(1 存储/2 张量)
             | dtype u8 | offset i64 | ndim u8
             | ndim * (dim u64, stride i64) | nelem u64 | sha256(数据摘要)
```

数据摘要 = `sha256(dtype代码 | ndim | 各维形状 | 按逻辑顺序读出的元素字节)`，
能发现任何元素字节、形状或步长的篡改。张量可相对另一张量引用（偏移累加），
从而允许“视图的视图”，但引用链必须终结于某个存储。

## 安全要点（对应需求）

- **预算先行**：`nbytes` 先与总字节预算比较，任何分配前即拒绝超大存储；
  存储载荷按 1 MiB 分块流式读入并同步哈希。
- **乘法溢出**：形状乘积用“除法上界”逐步检查（`n > MAX // d`），
  超大乘积（如 2^40 × 2^40）在到达分配器前拒绝；`nelem` 必须等于形状乘积。
- **完整地址可达范围**：对每个维度取 `0` 与 `(d-1)*stride`，求最小/最大字节地址，
  负步长正确把可达范围向偏移低端扩展；检查 `0 <= start` 且 `end <= 存储字节数`，
  并要求偏移与步长相对 `itemsize` 对齐。
- **共享存储 / 重叠视图身份**：编码按存储对象去重，多视图指向同一存储 id；
  恢复后用 `is` 可验证仍是同一个 `Storage`。
- **循环 / 悬空引用**：引用图解析用带路径集合的迭代递归，跨对象环和悬空引用均拒绝。
- **原子发布**：所有条目进入暂存区 → 解析引用 → 校验布局 → 校验全部摘要，
  全部通过后才构造并返回唯一的 `Catalog`；任何失败只抛 `CodecError`，
  不存在全局可变状态，不泄漏任何半成品。
- **截断**：精确读取 + 结尾多余字节检查，所有前缀截断均失败，无部分成功。

## 独立验证

- `independent_enumerate_bytes`：用与生产代码完全独立的递归索引枚举，
  收集每个逻辑元素触达的字节集合；随机 400 组（含负步长）布局与
  生产的 `reachable_byte_span` 闭式区间逐项比对。
- `independent_enumerate_bytes` 越界判定对 2000 组随机布局与
  `validate_layout` 的接受/拒绝判定做交叉一致性检查。
- 独立数值验证：用纯 Python 嵌套列表手工计算各视图期望值并逐元素比对，
  再用独立的 SHA-256 重算恢复值校验。
- 逐字段变异（magic、version、计数、dtype、ref_kind、ndim、存储摘要、
  张量摘要、重复 id、全部前缀截断、尾随字节）+ 500 次随机多字节翻转
  与 100 次随机截断；模糊中“被接受”的变异仍必须通过全部布局与摘要一致性。

## 运行样例

```
$ python3.11 demo.py
== build tensors over one shared storage ==
  base             shape=(4, 6) strides=(24, 4) offset=0
  rows reversed    shape=(4, 6) strides=(-24, 4) offset=72
  sub 1:3,2:6      shape=(2, 4) strides=(24, 4) offset=32
  every other col  shape=(4, 3) strides=(24, 8) offset=0
  both reversed    shape=(4, 6) strides=(-24, -4) offset=92

serialized 610 bytes for 5 views, 1 storage

== restored views ==
distinct backing storages after restore: 1 (shared identity preserved)
  tensor 1: [[0, 1, 2, 3, 4, 5], [6, 7, 8, 9, 10, 11], ...]
  tensor 2: [[18, 19, 20, 21, 22, 23], [12, 13, 14, 15, 16, 17], ...]
  tensor 3: [[8, 9, 10, 11], [14, 15, 16, 17]]
  tensor 4: [[0, 2, 4], [6, 8, 10], [12, 14, 16], [18, 20, 22]]
  tensor 5: [[23, 22, 21, 20, 19, 18], [17, 16, 15, 14, 13, 12], ...]

== attacks are rejected cleanly ==
  truncated stream -> CodecError: unexpected end of stream
  flipped storage byte -> CodecError: storage 1 checksum mismatch
  1 TB storage claim   -> CodecError: storage byte budget exceeded
  2^40 x 2^40 shape    -> CodecError: shape product overflow
```

## 测试结果

```
$ python3.11 -m unittest test_tensor_codec -v
...
----------------------------------------------------------------------
Ran 40 tests in 0.080s

OK
```

覆盖：负步长可达范围、正负越界、对齐拒绝、空张量、乘积溢出、
共享身份保持、10 种 dtype、文件流式往返、张量链引用、存储预算炸弹、
错位偏移、跨对象循环、悬空引用、nelem 不符、逐字段变异、
500 次随机翻转 + 100 次截断模糊、失败原子性、独立枚举器交叉验证、独立数值验证。
