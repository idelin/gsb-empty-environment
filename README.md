# schema-compat

有限的 schema 兼容性检查器与转换器（TypeScript，Node ≥ 23.6，无第三方依赖）。

## 类型系统

类型只含：`bool`、`int64`、`uint64`、UTF-8 `string`、`bytes`、嵌套 `record`、有限 `array`。
字段使用稳定数字 ID，`name` 仅作展示，不参与兼容性判断（重命名兼容）。
递归类型在结构上不可能（类型是内联树）；任何 `$ref` 形式的引用一律 `unsupported`。

```jsonc
{
  "kind": "record",
  "fields": [
    { "id": 1, "name": "count", "required": true,
      "type": { "kind": "int64", "min": 0, "max": 100 } },
    { "id": 2, "name": "tags", "required": false, "default": [],
      "type": { "kind": "array", "element": { "kind": "string", "maxLen": 16 }, "maxLen": 8 } }
  ]
}
```

- `int64`/`uint64` 必须显式给出 `min`/`max`（十进制数或字符串，按 BigInt 精确处理，且不得超出所属域）。
- `string`/`bytes`/`array` 必须显式给出 `maxLen`（string 按 Unicode 码点计，bytes 按字节计）。
- `required` 必须显式为布尔值；`default` 只允许出现在可选字段上，且必须对字段类型有效。
- 任何无法支持的规则（未知关键字、缺上限、递归引用、必填字段带默认值、非法默认值、重复 ID、区间越界等）都返回 `unsupported`，绝不默认兼容。

## 兼容性判定（新 schema 是否接受旧 schema 允许的所有值）

- **R1 必填性**：新 schema 必填的字段，旧 schema 必须保证存在（旧 schema 同 ID 必填）。
- **R2 默认值保持**：旧可选字段的默认值必须原样保留（同 ID、可选、默认值深相等），否则省略该字段的消息语义改变（“默认值失效”）。
- **R3 类型域包含**：同 ID 字段，旧类型域必须 ⊆ 新类型域（数值区间、长度上限、数组元素、record 结构，递归）。`int64`/`uint64` 跨 kind 按区间包含判定；`string`↔`bytes` 按消息层表示（base64 字符串）的实际域重叠判定。
- 新 schema 删除字段：兼容（转换器把被删字段保留进未知字段区域）。新增可选字段：兼容。

输出 `compatible` / `incompatible` / `unsupported`。不兼容时给出具体最小反例：
先按规范 JSON 编码的 UTF-8 总字节数、再按编码文本的 Unicode 码点序列字典序最小化。
规范编码：对象键按码点排序、无空白、整数十进制、bytes 为规范 base64。

## 转换器

- 按字段 ID 映射；未知字段（以及被新 schema 删除的字段）保留在每层 record 的独立 `unknown` 区域。
- 缺失 ≠ null：缺失的可选字段保持缺失（不物化默认值）；`null` 对任何类型都是非法值。
- 整条消息“验证 + 转换”原子完成：任何失败都不产生部分记录。

输出形态：`{ "fields": { "<id>": ... }, "unknown": { ... } }`（每层 record 同构）。

## 版本化缓存与热切换

`SchemaRegistry` 把编译后的 schema 与兼容性结论绑定到不可变版本号。
`begin()` 返回的 `Session` 固定使用开始时的版本；热切换（`register` + `setCurrent`）后，
在途会话继续按开始时的版本执行。

## CLI

```sh
node src/cli.ts check   old.json new.json            # 退出码 0/1/2 = 兼容/不兼容/unsupported
node src/cli.ts convert old.json new.json msg.json   # 退出码 0/1/2 = 成功/验证失败/schema错误
node src/cli.ts verify  old.json new.json            # 有限小域穷举，与检查器结果交叉验证
```

## 测试

```sh
node --test test/*.test.ts    # 需要 Node >= 23.6（原生类型擦除）
```

覆盖：字段重命名、各类缩窄、可选变必填、默认值失效、深层错误路径、
string↔bytes 域重叠、未知字段保留、原子性、热切换、以及随机 schema 对的
穷举交叉验证（检查器结果与最小反例必须和暴力枚举完全一致）。
