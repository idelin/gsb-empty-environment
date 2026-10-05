# CSE optimizer for a small language with memory and traps

公共子表达式消除（CSE）优化器 + 解释器 + 有限输入差分验证。
语言包含纯运算、内存读写和可能抛错的指令；别名写入与异常顺序使
"按文本比较表达式"的朴素替换不合法，优化器用值编号（value numbering）
配合保守的别名与陷阱规则保证可观察语义不变。

## The language (`src/lang.ts`)

直线式指令流（单个基本块），int32 回绕算术：

| 指令 | 语义 | 可能抛错 |
| --- | --- | --- |
| `const dst, k` / `mov dst, src` | 纯 | 否 |
| `bin dst, op, a, b` | add/sub/mul/and/or/xor/lt/le/eq/ne | 否 |
| `div` / `mod dst, a, b` | 截断除法 | `div-by-zero`（b = 0） |
| `load dst, [addr]` | 读内存 | `oob-load` |
| `store [addr], src` | 写内存（可观察） | `oob-store` |
| `assert cond` | cond = 0 时失败 | `assert-failed` |
| `ret src` | 返回 | — |

可观察语义（`src/interp.ts` 的 `Outcome`）= **返回值、写入轨迹
（store 的地址/值序列）、首个异常** 三元组。

## The optimizer (`src/opt.ts`)

单遍值编号 CSE。关键正确性规则：

1. **别名**：`store` 使所有"地址可能与之相等"的 `load` 缓存失效。
   仅当两个地址都是已知常量且不等时才可证无别名，否则保守清除。
   同一值编号的地址是 must-alias，支持 store-to-load 转发。
2. **异常顺序**：可能抛错的指令从不被删除、引入或跨指令移动。
   CSE 只把*后出现*的相同计算替换为对*较早*结果的拷贝——直线代码中
   能执行到第二个，说明第一个没抛错，第二个也不会。若反过来删早留晚，
   会把异常推迟到中间 store 之后，改变写入轨迹。
3. **常量折叠**：纯运算任意折叠；`div/mod` 仅在除数为已知非零常量时
   折叠；除数为常量 0 是必须保留的陷阱。
4. **assert**：条件已知非零（常量或同一值已被断言过）则删除；
   已知为零的断言是必须保留的陷阱。

优化器从不重排指令、从不增大程序，且幂等（差分测试中断言）。

## Differential validation (`src/difftest.ts`)

`src/gen.ts` 用种子随机生成偏向冗余计算、别名 load/store 对和陷阱指令
的程序。对每个程序，在有限输入集上（小域穷尽 3^3×2^4 = 432 组 +
16 组宽域随机样本）逐一比对原程序与优化后程序的三元组输出，任何
不一致都会打印反例（种子、程序、输入、两边结果）并以非零码退出。

## Running

需要 Node ≥ 23.6（或 Node ≥ 22.6 加 `--experimental-strip-types`），
直接运行 TypeScript，无第三方依赖：

```sh
node src/tests.ts        # 针对别名/异常顺序的手工单元测试
node src/difftest.ts 2000  # 2000 个随机程序 × 448 组输入的差分验证
```

最近一次验证：`10000 programs, 4480000 input pairs, 0 mismatches`。
