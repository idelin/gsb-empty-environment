# 模板沙箱名称解析隔离

一个可导入的 TypeScript 安全模板解释包。模板以 AST 形式输入，
只允许：文本、变量、字段索引、条件、固定列表循环、宏调用。
不可信参数（数据记录）无法借名字解析访问宿主全局。

## 运行测试

```sh
npm test   # node --experimental-strip-types --test
```

## 用法

```ts
import { renderTemplate, renderSafe } from './index.ts';

const output = renderTemplate(
  {
    macros: [
      { name: 'greet', params: ['who'], body: [
        { kind: 'text', value: 'Hello, ' },
        { kind: 'var', name: 'who' },
      ]},
    ],
    body: [
      { kind: 'call', name: 'greet',
        args: [{ kind: 'field', target: { kind: 'var', name: 'user' }, key: 'name' }] },
    ],
  },
  { user: { name: 'ada' } },          // 不可信数据
  { maxCallDepth: 64, maxOutputBytes: 1 << 20 },
);
```

`renderSafe` 返回 `{ ok: true, output } | { ok: false, error }`，
失败分支上不携带任何输出片段。

## 隔离保证

- **词法作用域**：名称沿环境帧链（`Map` 实现）查找；循环变量、
  宏参数只在自己的帧内可见，可遮蔽外层同名绑定。
- **宏闭包**：宏在定义处捕获环境，调用时实参按值传递，
  宏体看不到调用方的任何局部绑定（无动态作用域）。
- **数据净化**：不可信数据被深拷贝为无原型、冻结的纯数据记录。
  函数、getter、Proxy、类实例、循环引用、非有限数字一律拒绝；
  `__proto__` / `constructor` / `prototype` 键直接丢弃；
  Proxy 抛出的宿主异常统一转换为 `INVALID_DATA`。
- **字段只读**：字段访问只认记录的自有键，缺失即 `UNKNOWN_FIELD`，
  永远不会沿原型链解析到 `hasOwnProperty`、`toString` 等宿主方法；
  渲染记录/列表报 `TYPE_MISMATCH`，不触发任何 `toString`。
- **预算**：宏调用深度、输出 UTF-8 字节数、循环总迭代次数、
  数据节点数/深度均有上限，超限抛 `BudgetExceededError`。
- **失败原子性**：输出先累积在解释器私有缓冲中，只有整体成功
  才返回；任何失败都不会发布片段。

## 文件

- `src/ast.ts` — AST 类型与预算定义
- `src/errors.ts` — 错误体系（`TemplateError` / `BudgetExceededError`）
- `src/sanitize.ts` — 不可信数据净化器
- `src/interpreter.ts` — 词法环境解释器
- `test/interpreter.test.ts` — 递归宏、变量遮蔽、保留属性名、
  恶意数据记录、预算与失败原子性的隔离验证
