# 安全通知模板渲染器

供 TypeScript 通知服务直接导入的零依赖模板组件。模板只能引用声明过的变量、白名单过滤器与有限控制结构；属性读取只发生在无原型、冻结的只读视图上。

```ts
import { renderTemplate } from './index.ts';

const text = renderTemplate(
  'Hi {{ user.name | trim | capitalize }}!',
  { user: { name: '  ada  ' } },
);
// => "Hi Ada!"
```

## API

```ts
renderTemplate(template, context?, options?): string
compileTemplate(template, options?): callable + { ast, render(context?, overrides?) }
render(template, context?, options?): string
tryRenderTemplate(template, context?, options?): { ok, value | error }
new TemplateRenderer(options?)
createRenderer(options?)
```

也支持配置对象形式：

```ts
renderTemplate({
  template: '{{ name }}',
  data: { name: 'Ada' },
  variables: ['name'],
});
```

`TemplateError` 只暴露模板位置与稳定错误码，不包含变量值、模板片段或过滤器异常信息：

```ts
import { TemplateError } from './index.ts';
// message: "Template error at line 2, column 8"
// line / column / offset / code
```

## 模板语法

- 输出：`{{ user.name }}`、`{{{ raw }}}`
- 过滤器：`{{ name | upper }}`、`{{ name | truncate(20) }}`、`{{ name | default: "匿名" }}`
- 条件：`{{#if active}}...{{else if pending}}...{{else}}...{{/if}}`
- 反向条件：`{{#unless hidden}}...{{/unless}}`
- 循环：`{{#each items as item}}{{@index}}:{{item.name}}{{/each}}`
- 对象循环：`{{#each map as value}}{{@key}}={{value}}{{/each}}`
- 作用域：`{{#with user as account}}{{account.name}}{{/with}}`
- 循环元数据：`@index`、`@first`、`@last`、`@key`
- 父作用域与根作用域：`{{../title}}`、`{{@root.title}}`

内置过滤器：`upper`、`uppercase`、`lower`、`lowercase`、`trim`、`capitalize`、`escape`、`html`、`urlencode`、`length`、`default`、`json`、`truncate`、`replace`、`join`、`keys`、`values`、`slice`、`bool`。

宿主可显式注册过滤器；模板上下文永远不能提供或调用函数：

```ts
renderTemplate('{{ name | shout }}', { name: 'Ada' }, {
  filters: { shout: (value) => `${String(value)}!` },
});
```

## 安全模型

- 上下文在求值时转换成冻结、无原型的只读视图；原始对象不会被修改。
- 只读取对象自身的可枚举数据属性。getter、accessor、原型链、symbol 键均不会执行或暴露。
- 路径必须是字面量形式：点路径、数字下标或字符串字面量下标。动态下标、函数调用、构造器、原型、`__proto__`、`process`、`globalThis` 等逃逸入口在解析阶段拒绝。
- `variables` / `allowedVariables` / `declaredVariables` 可声明允许的根变量。
- 未提供变量声明时，上下文自身的可枚举自有键即为声明集合；生产环境建议显式传入声明列表。
- `allowedPaths` / `declaredPaths` / `whitelist` 可进一步限定完整路径，支持 `*` 与 `**`：

```ts
renderTemplate('{{ user.name }}', { user: { name: 'Ada' } }, {
  variables: ['user'],
  allowedPaths: ['user.name'],
});
```

路径白名单同样作用于聚合输出与过滤器：`{{ this | json }}`、`{{ user | json }}`、`{{ this | keys }}`、自定义过滤器收到的副本都只会包含被允许的路径，无法通过整体输出绕过白名单。

- 过滤器只能来自内置白名单或宿主 `filters`；`allowedFilters` 可再收紧。自定义过滤器收到的是冻结、无原型的深拷贝，不能修改上下文。
- 模板没有赋值语法，因此无法写入上下文或污染 `Object.prototype`。
- 缺失变量默认渲染为空字符串；`strictVariables: true` 时抛出只含位置的 `TemplateError`。

## 预算

默认预算可通过顶层选项或 `limits` 覆盖：

| 预算 | 默认值 | 选项 |
| --- | ---: | --- |
| AST/表达式节点 | 10,000 | `maxNodes` |
| 嵌套深度 | 64 | `maxDepth` |
| 总循环次数 | 10,000 | `maxIterations` |
| 输出长度 | 100,000 字符 | `maxOutputLength` |
| 模板长度 | 1,000,000 字符 | `maxTemplateLength` |
| JSON/值节点 | 10,000 | `maxValueNodes` |
| JSON/值字符串长度 | 100,000 字符 | `maxValueStringLength` |

超过任一预算会立即抛出 `TemplateError`，不会继续求值。

## 测试

```sh
npm test
```

测试覆盖合法模板、属性链逃逸、原型污染、函数调用、getter 副作用、超长循环、节点/输出预算、缺失变量、路径白名单与自定义过滤器只读视图。
