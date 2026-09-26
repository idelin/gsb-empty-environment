# tensorfusion — 逐元素融合引擎（纯标准库，Python 3.11）

在空仓库中从零实现：CPU 小张量、相邻纯逐元素算子融合、显式循环计划、
别名/版本依赖检测、双解释器差分验证、手写反向模式梯度与一致性验证。
不依赖任何自动微分或张量库（无 numpy/pytorch），仅使用标准库与 unittest。

## 模块结构

| 文件 | 职责 |
| --- | --- |
| `tensorfusion/tensor.py` | 张量：共享存储 + shape/strides/offset 视图（transpose/narrow/broadcast_to），存储级版本计数 |
| `tensorfusion/program.py` | 直线式 IR（add/sub/mul/div/neg、reduce_sum、mutate、store） |
| `tensorfusion/fuser.py` | 融合器：生成显式循环计划；别名/版本/归约屏障检测；非法融合返回具体证据 |
| `tensorfusion/interp.py` | 未融合与融合两个解释器；读写/缓冲统计；错误定位回原表达式 |
| `tensorfusion/autodiff.py` | 手写反向模式自动微分（含广播梯度归约） |
| `tensorfusion/verify.py` | 融合前后前向/梯度一致性；BITWISE/CLOSE/MISMATCH 三级判定；有限差分独立数值验证 |
| `tensorfusion/diff.py` | 小形状穷举差分：边界值（0、-0.0、次正规、inf、nan）× 广播形状组合 |

## 关键设计

- **融合安全**：只融合相邻纯逐元素算子；归约是屏障；`mutate`（原地写，版本号递增）
  之间的读写构成版本依赖；`store` 输出视图与输入共享存储且索引映射不一致时
  构成别名危害。`attempt_fuse` 对非法融合抛出带具体证据的 `IllegalFusion`
  （别名冲突的扁平位置、版本号变化、归约屏障），`compile_program` 则保守地
  只生成可证明安全的 kernel。
- **循环计划**：每个 kernel 生成显式嵌套循环计划（`format_plan` 可打印），
  广播通过对齐步幅（broadcast 维步幅为 0）实现。
- **错误定位**：融合 kernel 出错时回退到诊断回放（按原算子顺序逐算子、逐元素
  短路），保证报告的原表达式、输出索引、算子索引与未融合解释器完全一致；
  分母为 0 能定位到原表达式与具体操作数位置。
- **数值诚实**：`compare_tensors` 区分 BITWISE / CLOSE / MISMATCH；归约重排
  造成的差异只报 CLOSE，绝不声称逐位相同。纯逐元素融合保证逐位一致。
- **梯度一致性**：融合前后分别执行前向（融合侧以 trace 物化中间量），
  对同一 tape 做反向传播并逐位比较；另用中心有限差分做独立数值验证。

## 运行

```bash
python3.11 examples/run_demo.py        # 演示
python3.11 -m unittest discover -s tests -t .   # 测试
```

## 运行样例（节选）

```
=== 1. Fusion: explicit loop plan + buffer/read/write stats ===
kernel t3: shape=(2, 3)
  for i0 in range(2):
    for i1 in range(3):
      t1 = add(a[3*i0 + 1*i1], b[1*i1])
      t2 = mul(t1, c[3*i0 + 1*i1])
      t3 = div(t2, b[1*i1])
      write t3
unfused: buffers=4 reads=42 writes=19
fused  : buffers=2 reads=30 writes=7
buffer allocations reduced by 2 (4 -> 2)

=== 2. Division by zero located to the original expression ===
unfused: float division by zero in expression 't2 = div(t1, b)' at output index (0, 1); operand indices: t1(0, 1), b(1,)
fused:   float division by zero in expression 't2 = div(t1, b)' at output index (0, 1); operand indices: t1(0, 1), b(1,)

=== 3. Gradient-graph consistency before/after fusion ===
forward t1..t4: bitwise identical
grad a/b/c: bitwise identical
finite-difference check a/b/c: OK (max err ~1e-08)

=== 4. Reduction reordering is reported honestly (not bitwise) ===
forward-order sum = [1e-16], reversed-order sum = [2.220446049250313e-16]
verdict: numerically close (NOT bitwise identical): max|diff|=1.220e-16

=== 5. Illegal fusion returns concrete dependency evidence ===
[alias] output view (storage s183) overlaps input 'x' at flat positions [0, 1, 2, 3] ...
[version] in-place mutation 'x += d' bumps 'x' from version 0 to 1: 't1 = add(x, b)' reads the old version ...
[reduction] 't2 = reduce_sum(t1)' is a reduction barrier ...

=== 6. Exhaustive small-shape differential check ===
cases=2676 bitwise_ok=2526 error_cases=150 mismatches=0
buffers: unfused=6290 fused=2526 (saved 3764)
reads: unfused=39225 fused=30423; writes: unfused=20748 fused=9384
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -t .
............................
----------------------------------------------------------------------
Ran 28 tests in 0.172s

OK
```

覆盖：广播与视图/版本（test_tensor）、合法/非法融合与证据（test_fuser）、
双解释器逐位一致与错误定位（test_interp）、梯度一致性与归约诚实性
（test_verify）、2676 例穷举差分（test_diff）。
