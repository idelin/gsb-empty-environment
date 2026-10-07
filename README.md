# schema-compat

判断旧模式的所有合法记录能否被新模式接受；不兼容时返回**最小具体反例记录**。

## 模式模型（`src/schema.ts`）

- `enum`：有限枚举域，`values[0]` 为最小值
- `object`：封闭对象（拒绝未声明键），字段含 `required` / `nullable`
- `array`：`minLength` / `maxLength` 长度界 + 元素子模式
- `validate(schema, value)`：记录校验器

## 兼容判定（`src/compat.ts`）

```ts
import { checkCompatibility } from './src/compat.ts';

const result = checkCompatibility(oldSchema, newSchema);
// { compatible: true }
// 或 { compatible: false, counterexample, path, reason }
```

不兼容来源（每处生成候选反例，按结构大小取最小）：

1. **新增必填字段不可忽略**：旧记录可能缺失新必填字段
2. 旧有字段在新模式中未声明（封闭对象拒绝未知键）
3. 旧可空字段在新模式中禁止 `null`
4. 枚举域收缩（旧值不在新域中）
5. 数组长度界：`oldMin < newMin` 或 `oldMax > newMax`
6. 嵌套对象 / 数组元素的递归不兼容

有限域枚举：枚举本身有限；数组长度只需在边界点 `oldMin` 与
`newMax + 1` 处探测，因此嵌套结构的核对是有界枚举而非穷举。
反例构造后会用 `validate` 双向复核（旧接受、新拒绝）。

## 测试

需要 Node ≥ 22.18（原生 TypeScript 类型擦除）：

```sh
npm test
```
