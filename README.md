# SpiderMonkey域间视图隐藏内容属性

内容页脚本能在自己的对象上覆盖 toString、定义同名 getter 或替换原型，特权代码若直接读这些属性就会执行攻击者的函数。为 TypeScript 特权宿主写供跨隔离域访问调用的 Xray 式视图包：输入对象图描述（每个对象的所属域、原生属性表、内容域追加或遮蔽的属性、原型指针），特权域经视图读属性时只返回该对象类型的原生属性，内容域追加和遮蔽的属性不可见，原型按原生原型走；视图返回的对象继续包成视图；访问器属性不执行内容域函数，只报告其存在；内容域对象互相引用成环时视图展开不得死循环。用遮蔽 toString、追加 getter、替换 __proto__、环形引用和普通原生读取验证。

## 结构

- `src/graph.ts`：对象图描述类型与构造助手。对象之间只用 `{ $ref }` 指针关联，图是纯数据，读取图不会执行任何内容函数。
- `src/xray.ts`：`Xray` 视图核心。基于 `Proxy` 实现属性过滤、原生原型固定、只读约束与环安全缓存。
- `src/intrinsics.ts`：特权侧标准内置件（Object/Array/Function 原型与 `toString`、`call/apply/bind`、`join` 等实现）。
- `src/expand.ts`：环安全的视图快照展开，回边输出 `{ $cycle }`，超深节点输出 `{ $depthLimit }`。
- `test/content-mirror.ts`：内容域语义的最小模拟，用于对照证明 Xray 不触发内容函数而内容侧访问会触发。
- `test/xray.test.ts`、`test/security.test.ts`：规定场景与安全对抗测试。

## 核心语义

- 原生属性查找只沿 `nativeProto` 链，并用 `hasOwnProperty` 读取自有键，避免 `Object.prototype` 污染键被当作原生属性。
- 内容域的 `contentProps`（追加或遮蔽）对视图完全不可见；同名原生属性仍返回原生值。
- `__proto__` 与 `getPrototypeOf` 固定返回 `nativeProto` 视图，忽略 `contentProto`。
- 视图返回的引用值递归包成视图，按节点 id 记忆化，环回边命中缓存，不会无限递归。
- 访问器属性不调用任何 getter，返回冻结的存在性标记 `{ xrayAccessor: true, ownerNode, key }`。
- 视图只读：`set`、`deleteProperty`、`defineProperty`、`preventExtensions` 一律抛 `TypeError`。

## 用法

```ts
import { createStandardRuntime, ID, nativeData, contentAccessor, ref } from './index.ts';

const { xray } = createStandardRuntime([{
  id: 'pageObject',
  domain: 'https://attacker.example',
  intrinsic: 'Object',
  nativeProto: ID.ObjectPrototype,
  nativeProps: { name: nativeData('legit'), back: nativeData(ref('pageObject')) },
  contentProps: {
    toString: contentAccessor({ get: () => () => 'pwned' }),
  },
}]);

const view = xray.view('pageObject') as Record<string, unknown>;
view.name;            // 'legit'
view.toString;        // 特权侧原生函数视图，不执行内容 getter
view.back === view;   // true，环引用安全
```

## 测试

```sh
npm test
```

运行在 Node 原生 TypeScript 擦除模式（`--experimental-strip-types`），无需第三方依赖。
