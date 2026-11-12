# Kryo类ID注册表预检

Kryo 允许任意类时，攻击者能在流里写类标记触发危险类的反序列化；开启注册后仍要防止注册号被改成未注册值。为TypeScript网关实现供解码前调用的 Kryo 类标记预检包：本题约定类标记为变长整数，0 表示空，1 表示以类名字符串写出，其余值减 2 为注册号，调用方给出注册号到类的表；注册号未注册、使用类名写法、引用标记指向不存在的对象一律拒绝，变长整数超过 5 字节或集合长度超预算同样拒绝。用注册号错位、类名写法夹带、截断的变长整数和合法流验证。

## 用法

```ts
import { preflight, assertPreflight, type ClassDef } from './index.ts';

const registry: ReadonlyMap<number, ClassDef> = new Map([
  [0, { kind: 'scalar', byteLength: 4 }], // 定长载荷
  [1, { kind: 'collection' }],            // 长度前缀 + 嵌套节点
  [2, { kind: 'reference' }],             // 指向已出现对象的引用
]);

const result = preflight(bytes, registry, { maxCollectionLength: 1024, maxDepth: 64 });
if (!result.ok) {
  // result.error.code: UNREGISTERED_CLASS | CLASS_NAME_NOT_ALLOWED | DANGLING_REFERENCE
  //                  | VARINT_TOO_LONG | TRUNCATED_VARINT | TRUNCATED_PAYLOAD
  //                  | COLLECTION_TOO_LARGE | DEPTH_EXCEEDED
  return reject(result.error);
}
decode(bytes); // 预检通过后再交给解码器
```

- 类标记为无符号变长整数（7 位/字节，最多 5 字节）：`0` 空值，`1` 类名写法（拒绝），`n ≥ 2` 对应注册号 `n - 2`。
- 引用载荷是已读对象序号，指向尚未出现的对象即拒绝。
- `preflight` 返回结果联合类型不抛异常；`assertPreflight` 在拒绝时抛 `PreflightError`。

## 测试

```sh
npm test
```
