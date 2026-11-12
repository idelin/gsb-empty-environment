# 多态 JSON 类型白名单

面向 TypeScript 服务的严格多态 JSON 解码组件，兼容 kotlinx.serialization
风格的 `{"type": "...", ...}` 多态载荷。每个基类拥有独立的白名单注册表，
未注册的序列化名不会被猜测或回退。

## 使用

```ts
import { f, polymorphic } from "./index.ts";

type Shape =
  | { kind: "circle"; radius: number }
  | { kind: "rect"; width: number; height: number };

const ShapeBase = polymorphic<Shape>();

ShapeBase.register("circle", {
  fields: { radius: f.number() },
  decode: ({ radius }) => ({ kind: "circle", radius }),
});

ShapeBase.register("rect", {
  fields: { width: f.number(), height: f.number() },
  decode: ({ width, height }) => ({ kind: "rect", width, height }),
});

// 传入原始 JSON 文本，才能检测重复键。
const shape = ShapeBase.decode('{"radius":2,"type":"circle"}');
// => { kind: "circle", radius: 2 }
```

子类对象也可以作为字段解码器使用，因此嵌套多态只需直接引用基类：

```ts
ShapeBase.register("group", {
  fields: { members: f.array(ShapeBase) },
  decode: ({ members }) => ({ kind: "group", members }),
});
```

## 安全语义

- `type` 必须恰好出现一次、必须是字符串，可以位于对象任意位置；缺失或类型错误均拒绝。
- 未知序列化名直接抛出 `UNKNOWN_TYPE`，没有默认子类或猜测逻辑。
- 使用内置 JSON 解析器而非 `JSON.parse`，在任意嵌套层级拒绝重复键（包括 `"type"` 与 `"\u0074ype"` 这类转义后同名的键）。
- 子类字段采用白名单校验：任何未声明字段抛出 `UNKNOWN_FIELD`，必填字段缺失抛出 `MISSING_FIELD`。
- 注册子类时若 `fields` 中声明了 `type`，立即抛出 `DISCRIMINATOR_CONFLICT`，且不会写入注册表。
- 同一基类下重复的序列化名抛出 `DUPLICATE_SERIAL_NAME`。
- 多态嵌套最多 6 层，顶层多态值为第 1 层；数组中的多态值同样计入深度，超过时抛出 `DEPTH_LIMIT`。
- 重复键检测依赖原始文本。直接传给 `decode` 的已解析 JavaScript 对象无法保留重复键，生产入口应优先使用字符串输入。

## 字段解码器

`f` 命名空间提供：

- `f.string()`、`f.number()`、`f.boolean()`
- `f.literal(...)`：仅允许列出的字面量
- `f.array(decoder)`：逐项解码
- `f.optional(decoder)`：字段缺失时为 `undefined`
- `f.nullable(decoder)`：允许 `null`
- `f.object(fields, build?)`：嵌套严格对象，未知字段同样拒绝

自定义解码器只需实现 `decode(value, context)`，可以读取 `context.path`
生成带位置的错误。所有错误均为 `PolymorphicDecodeError`（含 `code` 和
`path`），注册错误为 `PolymorphicRegistrationError`（含 `code`）。

## 测试

```sh
npm test
```

测试覆盖未注册类型、`type` 居后、重复 `type`、转义重复键、子类未知字段、
`type` 字段注册冲突以及第 6/7 层嵌套边界。
