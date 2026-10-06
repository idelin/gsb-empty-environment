# ts-lexer

一个支持流式分块、暂停/恢复检查点和增量编辑的 TypeScript 风格词法库。

## 词法规则

- **字符串**：双引号界定，`\` 转义下一个字符（`\"`、`\\` 等）；注释符在字符串内无效。
- **块注释**：`/* ... */`，可任意嵌套（`/* /* */ */` 是一个完整注释）。
- **行注释**：`//` 至行尾，token 包含结尾换行符；引号与注释符在注释内无效。
- 其余内容归为 `code` token（极大连续段）。token 流无缝铺满整个输入。
- 所有偏移为**绝对 UTF-16 code unit 偏移**（即 JS 字符串下标，emoji 等代理对计 2）。

## API

### `ChunkLexer` — 流式分块词法器

```ts
const lexer = new ChunkLexer();
lexer.feed('let s = "he');   // 任意切块，可切在 //、/*、*/ 两字符之间
lexer.feed('llo" /* a');
lexer.feed(' /* b */ */');
const unterminated = lexer.finish(); // => Unterminated[]
lexer.tokens;                        // => Token[]，含绝对 UTF-16 范围
```

`finish()` 报告所有未闭合起点：未闭合字符串的起点，以及嵌套栈里每个未闭合 `/*` 的起点。

### 暂停 / 恢复检查点

```ts
const cp = lexer.checkpoint();        // 纯 JSON 数据：未完成字符串、嵌套注释栈、源偏移
const resumed = ChunkLexer.resume(JSON.parse(JSON.stringify(cp)));
resumed.feed(rest);                   // 与连续扫描结果完全一致
```

### `IncrementalDocument` — 增量词法

```ts
const doc = new IncrementalDocument(text);
doc.applyEdit({ start, end, newText }); // 坐标按当前（旧）版本给出
doc.getTokens();                        // 与全量重扫一致
doc.getDiagnostics();                   // 未闭合起点
doc.getLastRescanChars();               // 上次编辑实际重扫的字符数
```

重扫从编辑点前最后一个**真实词法状态检查点**（而非固定回扫几行）开始，
持续到词法状态与 token 边界同时与旧流（按编辑量平移后）对齐为止，
之后的尾部 token 直接复用。检查点记录的是真实机状态——包括 code 段
懒冲刷导致的 `codeSlash` 等待态——因此对注释符首字符的逐字符删改也能
正确地向远端传播注释状态。

## 构建与测试

```sh
npm run build   # tsc -> dist/
npm test        # 构建并运行全部测试（约 17k 项断言）
```

测试覆盖：

- 基本规则：字符串内注释符无效、转义、嵌套块注释、行注释、UTF-16 偏移。
- **切块枚举**：对记号密集字母表上所有长度 ≤ 4 的字符串枚举全部二切分；
  对较长样例枚举全部二/三切分，与一次读取结果逐一比较。
- **暂停/恢复**：每个样例的每个切点做 JSON 序列化往返后恢复，结果与连续扫描一致。
- **增量编辑**：逐字符键入/删除 `/*` 验证远端注释状态传播；旧版本坐标连续编辑；
  随机模糊（随机文本 + 随机增删改，逐步与全量重扫比对 token、诊断与铺满性）；
  对记号密集文本做逐字符删除/插入扫描。
- **重扫停止**：稳定尾部上的小编辑重扫字符数有界；制造未闭合注释时重扫直达 EOF。
