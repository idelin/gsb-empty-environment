# 浏览器隔离对象跨域品牌校验

安全浏览器宿主将受信对象包装后交给脚本，脚本复制同名属性不能伪造对象能力。实现可导入的TypeScript包装库，能力身份保存在宿主私有WeakMap，所有方法校验真实接收者、所属文档和撤销代；跨域包装只暴露声明的操作，嵌套返回对象继续包装。用伪造原型、借用方法、撤销和文档导航测试不能绕过品牌校验。

## 设计概览

库实现一个宿主侧的能力膜（capability membrane）。受信目标对象永远不交给脚本，
脚本只拿到 `Proxy` 包装；所有能力身份保存在 `Membrane` 实例的私有字段中
（`#brands` / `#targets` 两个 `WeakMap`），脚本无法引用这些表、记录或原始目标。

- `src/errors.ts`：统一的 `SecurityError`，所有品牌/策略/撤销失败都抛该类型。
- `src/types.ts`：`ExposeSchema` 声明式跨域白名单与 `WrapOptions`。
- `src/membrane.ts`：核心膜。代理陷阱、品牌校验、撤销代、文档导航、白名单。
- `src/host.ts`：宿主门面 `Host`，只暴露包装/撤销/导航/诊断接口。
- `index.ts`：可导入入口。

## 能力身份与三重校验

每次属性读取或方法调用都依次校验：

1. **真实接收者（品牌）**：`this`/`receiver` 必须是该记录签发时的精确代理，
   通过私有 `WeakMap` 识别。复制同名属性、`Object.create(wrapper)` 伪造原型、
   借用 `call/apply/bind`、自建 `Proxy` 都因不在品牌表中而失败。
2. **所属文档**：记录绑定一个文档令牌；`host.navigate(doc)` 后该文档下所有包装
   立即失效，其它文档的包装不受影响。
3. **撤销代（generation）**：`host.revokeAll()` 递增撤销代，所有已签发包装失效；
   之后重新包装同一目标会得到新一代的新代理，旧缓存不会被复用。

## 跨域白名单与嵌套包装

- `host.wrap(target, doc)`：同源全能力包装（仍受品牌、撤销、文档约束）。
- `host.wrapCrossOrigin(target, doc, expose)`：仅暴露 `expose` 声明的键；
  未声明的键在 `get`/`has`/`ownKeys`/`getOwnPropertyDescriptor` 上全部隐藏，
  且包装只读（`set`/`deleteProperty`/`defineProperty`/`preventExtensions` 拒绝）。
- 嵌套对象与方法返回值继续按需包装并缓存，策略沿膜传播。
- 方法返回对象的暴露面用元键 `$return` 声明；未声明时返回对象默认完全受限，
  避免“方法可调用 → 返回对象全字段可读”的提权。
- 写入膜的包装参数会先 `unwrap`，可信代码只看到原始目标，永远拿不到代理。
- 真实原型被隐藏（`getPrototypeOf` 返回 `null`），`Function.prototype.call/apply/bind`
  原样透传，其接收者参数在 `apply` 陷阱中再次校验。

## 用法

```ts
import { Host, SecurityError } from './index.ts';

const host = new Host();
const document = { url: 'https://example.com' };

const trusted = {
  vault: { secret: 'no', title: 'ok', read() { return this.title; } },
  ping(m: string) { return `pong:${m}`; },
};

// 跨域：只给声明过的操作；vault.secret 永远不可达。
const api = host.wrapCrossOrigin(trusted, document, {
  ping: true,
  vault: { read: true, title: true },
});

api.ping('hi');                 // 'pong:hi'
api.vault.read();               // 'ok'
// api.vault.secret            -> SecurityError
// api.vault.read.call({})     -> SecurityError（伪造接收者）

host.revokeAll();               // 旧代理全部失效
host.navigate(document);        // 文档导航后该文档能力失效
```

## 测试

测试位于 `test/security.test.ts`，覆盖：伪造原型/形似对象、方法借用
（`call`/`apply`/`bind`/`Reflect`）、跨包装互借、自建代理伪装、撤销代、
文档导航、跨域白名单与内省陷阱、`$return` 策略、参数解包与目标不泄漏。

```sh
npm test
# 或在可用的 Node（>= 22.6，带类型剥离）上直接运行：
node --experimental-strip-types --test test/*.test.ts
```
