# JSON 树查询编译器（TypeScript）

从空仓库实现的 JSONPath 风格查询引擎：字段、切片、递归下降、谓词过滤，
支持**流式增量输出**、**版本化文档的增量结果维护**与**三阶段查询预算**。
纯 TypeScript，零依赖，Node.js ≥ 23.6 直接运行（原生类型擦除），测试使用 Node 内置 `node:test`。

```bash
npm test            # node --test test/*.test.ts
node examples/demo.ts
```

## 查询语言

```
$.store.book[0].title        字段 / 下标
$['a/b']['x y']              含斜杠、空格等特殊字符的键（括号记法）
$.a[1:8:2]  $.a[-1]  $.a[::-1]   切片（Python 语义）/ 负下标
$..author   $..*               递归下降（同路径命中自动去重）
$.a[?(@.x >= 2 && @.y != 's')]   谓词：比较 && || ! 括号 存在性
$.a[?(@.x < $.limit)]        谓词内引用文档根 $
```

- 输入为 JSON 兼容树；**对象键无序、数组有序**。
- 每条结果携带**规范路径**（`$["store"]["book"][0]["title"]`，键经 JSON 转义，
  斜杠/引号/空键均可精确往返，见 `canonical()` / `parseCanonical()`）。
- 谓词编译为**纯闭包**，从不经过宿主 `eval`/`Function`；
  注入式查询串只会得到带位置的 `ParseError`。

## 架构

| 模块 | 职责 |
| --- | --- |
| `src/jsonpath/parser.ts` | 递归下降解析器，错误携带查询串位置；消耗 `parse` 预算 |
| `src/jsonpath/compiler.ts` | AST → 执行计划（谓词闭包、`hasRootRef` 全局信息标记） |
| `src/jsonpath/interpreter.ts` | 参考解释器（批式、迭代实现）；流式/增量必须与其结果一致 |
| `src/jsonpath/events.ts` | JSON 树 → 深度优先 enter/exit 事件流（显式栈，深树安全） |
| `src/jsonpath/stream.ts` | 流式引擎：状态机 + 有界暂存 + 溢写 + 取消清理 |
| `src/jsonpath/incremental.ts` | 版本化文档（结构共享）、编辑原语、结果差量、稳定游标 |
| `src/jsonpath/budget.ts` | `parse` / `expand` / `compare` 三阶段预算 |

## 流式执行的确定性规则

- **可提前确定的结果增量输出**：字段、通配、非负下标/切片、局部谓词
  在对应子树闭合时立即经 `onMatch` 回调输出，无需等待整树。
- **不能过早确定的尾部索引**：负下标、负切片、反向步长需等容器闭合，
  引擎为该容器开启**有界暂存**（默认 1024 条，`maxBuffered` 可调）。
- **需要全局信息的谓词**（引用 `$`）：子树有界暂存并延期到根闭合后求值。
- 暂存超出上限：配置 `spillDir` 则**明确溢写**临时 JSONL 文件并在结束时读回；
  未配置则抛 `BufferOverflowError`（携带容器规范路径）。
- `AbortSignal` 取消：抛 `CancelledError`，**临时文件全部清理**（finally 保证）。

## 增量维护与稳定游标

- `VersionedDocument.apply(edits)`（set/delete）产生新版本；旧版本根通过
  结构共享保留且不可变。
- `IncrementalResults.sync()` 输出 `added / removed / changed` 差量。
- 游标 = 路径 + 绑定版本。**删除数组元素后，旧游标仍解析旧版本中的原节点**，
  不受下标位移影响。

## 错误定位

所有错误继承 `QueryError` 并尽量携带规范路径：`ParseError`（查询串位置）、
`BudgetExceededError`（阶段 + 路径）、`BufferOverflowError`（容器路径）、
`EditError`（编辑路径）、`CancelledError`。

## 测试

`test/` 下 6 个文件、40+ 用例：解析器、参考解释器、流式引擎（提前输出 /
负下标延迟 / 溢写 / 取消清理 / 深树）、增量差量与游标、
随机树+随机编辑下三者一致性对拍、安全（无 eval、原型污染免疫）。
