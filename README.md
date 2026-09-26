# minitensor

纯标准库（Python 3.11+）手写 CPU 小张量库 + 加固的流式序列化格式。
不依赖自动微分或任何第三方张量库。

## 功能

- **张量核心** (`minitensor/tensor.py`)：`Storage`（共享字节缓冲）+
  `Tensor(storage, dtype, shape, strides, offset)` 视图；stride 可为负，
  多视图可重叠共享同一存储；构造时完整校验地址可达范围。
- **序列化** (`minitensor/format.py`)：保存 dtype、shape、stride 布局、
  字节偏移与每个存储的 SHA-256 数据摘要。
- **独立验证** (`minitensor/verify.py`)：与主实现不共享代码的递归地址
  枚举器、参考视图物化、参考矩阵乘，用于交叉验证。

## 线格式

```
MAGIC        4 字节   b"MNT1"
header_len   8 字节   小端无符号，JSON 头长度（上限 1 MiB）
header       JSON     {"version":1, "storages":[...], "tensors":[...]}
payload      各根存储的原始字节，按头部顺序拼接
```

存储条目可以是根 `{"id","nbytes","sha256"}` 或共享引用
`{"id","alias_of"}`；多张量可指向同一存储 id。

## 恢复协议（失败即关闭，原子发布）

1. 读取并解析有界头部，逐字段校验类型与取值范围；
2. 解析存储引用图：允许共享/别名，拒绝循环与悬空引用（`CycleError`）；
3. **在读 payload 之前**用溢出受检乘法核对元素数与字节预算
   （`BudgetExceededError`，杜绝分配炸弹与 shape 乘积溢出）；
4. 精确读取各根存储字节：截断（`TruncatedError`）与尾随垃圾均失败；
   校验 SHA-256 摘要（`DigestMismatchError`）；
5. 校验每个视图：偏移对齐、含负 stride 的可达地址范围必须落在存储内
   （`ViewError`）；
6. 全部通过后才一次性构造并发布所有张量——任何一步失败都不泄漏
   半成品。

## 运行

```bash
python3 -m unittest discover -v   # 33 个测试
python3 examples/demo.py          # 端到端演示
```

## 运行样例（`python3 examples/demo.py` 摘要）

```
== 3. Restore with a byte budget; sharing identity preserved ==
restored matrix: [[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]]
restored flip  : [6.0, 4.0, 2.0]
shares storage : True
write via flip -> matrix[1,2] = 66.0

== 4. Independent numerical verification (matmul) ==
tensor matmul  : [[126.0, 188.0], [302.0, 476.0], [478.0, 764.0]]
reference      : [[126.0, 188.0], [302.0, 476.0], [478.0, 764.0]]
numerical check: PASS

== 5. Hostile inputs are rejected atomically ==
  rejected [TruncatedError      ] truncated payload
  rejected [DigestMismatchError ] digest mismatch
  rejected [ViewError           ] misaligned offset
  rejected [BudgetExceededError ] shape overflow (2^32 x 2^32)
  rejected [BudgetExceededError ] allocation bomb (1e9 x 1e9)
  rejected [CycleError          ] alias cycle
  rejected [ViewError           ] negative stride underflow
all attacks rejected; no partial tensors published
```

## 测试结果

```
$ python3 -m unittest discover -v
... (33 项：dtype 往返、视图/负 stride/重叠写穿、随机视图 vs 独立枚举器
 300 例交叉验证、矩阵乘 vs 纯 Python 参考、28 项逐字段变异拒绝、
 截断/尾随垃圾/坏 magic、预算与溢出、循环引用、原子发布) ...
Ran 33 tests in 0.011s
OK
```

## 目录

```
minitensor/
  dtypes.py    定长 dtype（struct 小端）
  tensor.py    Storage / Tensor / 地址边界校验
  format.py    序列化 + 加固恢复（预算、摘要、循环检测、原子发布）
  verify.py    独立地址枚举器与数值参考实现
tests/         unittest：核心语义 + 格式硬化
examples/demo.py  端到端演示
```
