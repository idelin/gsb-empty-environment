# 列式批量向量化表达式求值内核

内存列存批量计算引擎（TypeScript / Node.js，零依赖）。以定长数组
（Chunk/Vector，1024 行）为处理单位，在紧凑连续内存上完成批量表达式求值。

## 结构

- `src/vector.ts` — 定长列存数据块 `Int32Vector`（默认 1024 行），
  底层为单一连续 `Int32Array`，元素紧密排列、无指针间接，
  满足 CPU 预取与 SIMD 向量 load/store 的对齐要求。
- `src/kernels.ts` — 向量化内核：`add` / `mul` / `addScalar` / `mulScalar`
  及融合内核 `mulAddScalar`（单次遍历完成 `A * B + k`，无中间结果写回）。
  循环体只含纯算术、按 8 路展开，便于 JIT 消除边界检查并生成 SIMD 指令。
- `src/engine.ts` — 批量求值引擎：列按 1024 行切 Chunk，逐 Chunk 调用内核。
- `src/rowwise.ts` — 逐行迭代（火山模型）基线，用于性能对照。
- `bench/benchmark.ts` — 运行样例 + 吞吐基准。
- `test/engine.test.ts` — Node.js 内置测试（`node:test`）。

## 运行

要求 Node.js ≥ 22.6（原生 TypeScript 类型擦除，无需编译）。

```bash
npm test    # 运行测试
npm run bench   # 运行样例 + 基准
```

## 运行样例输出

```
表达式: A * B + 10, 行数: 1024, Chunk 数: 1
前 8 行结果: [10, -3567, -7130, -10679, -14214, -17735, -21242, -24735]
校验: A[0]*B[0]+10 = 10, 引擎输出 = 10

基准: 20000 次迭代 x 1024 行
向量化批量内核                      1460.0 ms  14.0 M rows/s
逐行迭代模型                       5895.9 ms  3.5 M rows/s

吞吐提升: 4.04x
```

## 测试结果

```
✔ Vector 默认 1024 行定长，内存紧凑连续
✔ 列加法内核 add
✔ 列乘法内核 mul
✔ 标量内核 addScalar / mulScalar
✔ 融合内核 mulAddScalar 计算 'A * B + 10'
✔ 引擎批量执行 'A * B + 10'（1024 行两列）
✔ 引擎跨多个 Chunk 处理任意长度列
✔ 向量化结果与逐行（火山模型）基线完全一致
✔ 长度不匹配时抛出错误
✔ 吞吐性能：向量化数倍快于逐行迭代模型
    [perf] vectorized=302.6ms row-wise=1923.4ms speedup=6.36x (200 iters x 65536 rows)

ℹ tests 10  ℹ pass 10  ℹ fail 0
```
