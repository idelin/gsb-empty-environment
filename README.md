# Columnar Vector Kernel

内存列存批量计算引擎：以定长数组（Chunk/Vector，1024 行）为处理单位，
在紧凑连续内存上执行列加法、列乘法与标量运算，循环对 CPU 缓存友好，
缓冲区 64 字节对齐（SIMD 友好）。

## 结构

- `src/vector.ts` — 定长列存向量（`Int32Array`，1024 行/chunk，64B 对齐分配）
- `src/kernels.ts` — 向量化内核：`add` / `mul` / `addScalar` / `mulScalar` / 融合算子 `mulAddScalar`（`A * B + 10` 单次遍历）
- `src/executor.ts` — 三种执行模型：
  - `VectorizedExecutor`：按 chunk 批量执行融合内核
  - `VolcanoExecutor`：经典火山模型迭代器（逐行 `next()`，null 终止）
  - `InterpretedRowExecutor`：逐行表达式解释器（栈机，每行重新解释操作码）
- `src/benchmark.ts` — 吞吐基准（best-of-5 轮，过滤 JIT/GC 噪声）
- `src/sample.ts` — 可运行样例
- `test/kernel.test.ts` — Node.js 内置测试（`node --test`）

## 运行

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译）。

```bash
npm test          # 运行测试
npm run sample    # 运行样例（含基准）
npm run bench     # 仅运行吞吐基准
```

## 验收：A * B + 10（两列各 1024 个整数）

样例输出：

```
Input: two Int32 columns of 1024 rows each
A[0..4] = [583, 255, 507, 445, 367]
B[0..4] = [669, 289, 847, 716, 631]

Expression: A * B + 10
out[0..4] = [390037, 73705, 429439, 318630, 231587]
check: 583 * 669 + 10 = 390037
aligned to 64B: A=true B=true out=true

--- Throughput benchmark ---
vectorized   : 661.8 M rows/s
volcano      : 162.7 M rows/s
interpreter  : 16.3 M rows/s
speedup vs volcano     : 4.07x
speedup vs interpreter : 40.54x
```

测试结果（10 个用例全部通过，含吞吐断言）：

```
✔ vectors are fixed-length chunks of 1024 int32 in contiguous memory
✔ vectors are 64-byte aligned for SIMD-friendly access
✔ column add: out[i] = a[i] + b[i]
✔ column mul: out[i] = a[i] * b[i]
✔ scalar ops: addScalar and mulScalar
✔ acceptance: A * B + 10 over two 1024-int columns
✔ all execution models agree on A * B + 10
✔ fused kernel matches composed add(mul(a,b), 10)
✔ throughput: vectorized model is several times faster than row-at-a-time models
✔ Vector.from round-trips plain arrays
ℹ pass 10  ℹ fail 0
```

多次运行加速比稳定区间：vs 火山迭代器 3.5x–10x，vs 逐行解释器 8x–40x。
