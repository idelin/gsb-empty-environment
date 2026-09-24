# annotation-ranges

随编辑移动的批注区间引擎（TypeScript，零依赖，Node.js ≥ 22.6 直接运行）。

## 特性

- **批注随编辑移动**：插入/删除/替换后，所有批注区间自动变换。
- **端点粘附方向**：左右端点各自可选 `inward`（向内）或 `outward`（向外）。
  例：批注 `[2,4)` 左端插字，向内策略下不纳入标注（变为 `[4,6)`），向外策略下纳入（保持 `[2,6)`）。
- **零宽锚点**：批注覆盖的内容被全部删除时，批注折叠为零宽锚点保留在删除点，不会被移除。
- **批量编辑**：一批编辑以原始文档坐标给出，按 `(start, 操作ID)` 升序应用；
  同位置插入的最终文本顺序与操作ID一致；后续编辑坐标随已应用编辑自动变换。
- **UTF-16 偏移**：所有公开偏移为 UTF-16 code unit 偏移（与 JS 字符串下标一致）；
  编辑与批注边界自动对齐，绝不切开代理对（如 emoji）。
- **LF / CRLF**：换行只是普通字符（`\r\n` 占 2 个偏移），两种行尾均有测试覆盖。
- **无负长度**：覆盖删除、零宽锚点插字等场景均保证 `start <= end`。
- **撤销**：`undo()` 恢复编辑前的文本与全部锚点，支持连续撤销。

## 使用

```ts
import { AnnotatedDocument } from './src/document.ts';

const doc = new AnnotatedDocument('abcdef');
const id = doc.addAnnotation(2, 4, 'outward', 'inward'); // 左端向外、右端向内

doc.applyEdit({ id: 1, start: 2, end: 2, text: 'XY' });   // 左端插入 -> 纳入批注
doc.applyBatch([                                          // 批量：按 (start, id) 排序
  { id: 2, start: 0, end: 1, text: 'H' },
  { id: 3, start: 5, end: 6, text: '' },
]);
console.log(doc.getAnnotation(id));                       // 变换后的区间
doc.undo();                                               // 恢复批量编辑前的文本与锚点
```

## 运行

```sh
npm test    # Node.js 内置测试运行器（node:test）
npm run demo  # 运行样例 examples/demo.ts
```

## 语义细节

- 纯插入落在端点上时，由该端点粘附方向决定是否纳入：`inward` 不纳入、`outward` 纳入。
- 删除/替换区间内部的端点塌陷到删除点；删除边界上的端点分别贴到塌陷点或替换文本之后。
- 两端均为 `inward` 的零宽锚点处发生插入时，锚点保持在插入文本之前（不产生负长度）。
- 编辑区间若切开代理对：插入点对齐到代理对之前；删除/替换扩展为包含整对。
- `undo()` 以快照方式恢复文本与全部批注锚点。

## 测试结果

`node --test "src/**/*.test.ts"`：19 个测试全部通过，覆盖端点粘附、零宽锚点、
批量操作ID排序、UTF-16/代理对、LF/CRLF、负长度防护与撤销。
