# lww-field-store

TypeScript 实现的字段级 LWW(Last-Writer-Wins)文档存储。

## 语义

- 每个操作携带字段路径、逻辑版本号和客户端 id。
- 每个字段独立比较:先比 `version`,相等再比 `clientId`,取最大者。
- 删除写入墓碑(tombstone),版本更低的旧值无法复活。
- 对象(set 一个 plain object)按叶字段递归分解合并;数组视为叶子,整体替换。
- 祖先/后代路径冲突按 stamp 解决:stamp 更高者遮蔽另一方,与到达顺序无关。
- `snapshot()` / `LwwDocument.restore()` 可序列化恢复;`merge()` 与消息重放均幂等、收敛。

## 使用

```ts
import { LwwDocument } from './src';

const doc = LwwDocument.create();
doc.apply({ kind: 'set', path: ['user'], value: { name: 'a', age: 1 }, version: 1, clientId: 'c1' });
doc.apply({ kind: 'set', path: ['user', 'name'], value: 'b', version: 2, clientId: 'c2' });
doc.apply({ kind: 'delete', path: ['user', 'age'], version: 2, clientId: 'c2' });
doc.value(); // { user: { name: 'b' } }
```

## 构建与测试

环境无网络,`npm run build` 直接引用本机已有的 TypeScript 5.9.3 编译器;`npm test`
编译后运行 `test/lww.test.ts`,对短操作序列做全排列,验证删除后旧值不复活、
独立字段合并、恢复与重放收敛。
