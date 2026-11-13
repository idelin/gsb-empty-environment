# 反向代理注解渲染包

入口控制器把资源注解值拼进反向代理配置时，不能信任注解值。这个包把「注解键值 +
目标指令模板 + 调用方指令白名单」渲染成一份完整配置，并在返回前用括号/分号状态机
复核整份配置。

## 快速使用

```ts
import { renderAnnotations } from './index.ts';

const templates = [
  {
    annotation: 'nginx.ingress.kubernetes.io/proxy-body-size',
    directive: 'client_max_body_size',
    type: 'size',
  },
  {
    annotation: 'nginx.ingress.kubernetes.io/proxy-pass',
    directive: 'proxy_pass',
    type: 'path',
  },
];

const config = renderAnnotations(
  {
    'nginx.ingress.kubernetes.io/proxy-body-size': '10m',
    'nginx.ingress.kubernetes.io/proxy-pass': '/api/v1',
    'nginx.ingress.kubernetes.io/unknown': 'dropped',
  },
  templates,
  ['client_max_body_size', 'proxy_pass'],
);
```

输出：

```nginx
client_max_body_size 10m;
proxy_pass /api/v1;
```

## 值类型

所有值先经过全局禁用字符检查，再按模板声明的类型校验：

- `token`：只允许 `[A-Za-z0-9._-]`，且至少一个字符。
- `size`：`数字` 加可选的 `k` 或 `m` 后缀，例如 `10`、`10k`、`24m`。
- `path`：必须以 `/` 开头，且任一路径段不能是 `..`。

任何类型都禁止换行、回车、NUL、分号、左右花括号、反引号、`$`，以及去掉前导空白后
以 `#` 开头的值。

## 模板

模板可以是对象数组，也可以是以注解键为键的对象。每个模板必须声明 `type`，并提供
`template`/`format`，或提供 `directive` 让包生成 `directive {{value}};`。模板占位符
支持 `{{value}}`、`${value}`、`%s` 等；也可以用 `render(value)` 自定义渲染。

```ts
const templates = {
  'example.com/token': {
    directive: 'proxy_set_header',
    type: 'token',
    template: 'proxy_set_header X-Feature {{value}};',
  },
};
```

## 复核与白名单

`renderAnnotations` 默认在返回前执行 `inspectConfig`：

- 花括号与圆括号深度必须回到 `0`，且不能出现提前闭合。
- 每条指令必须以 `;` 结束，块头必须是 `指令 参数 {`。
- 引号与注释内的分号/花括号不会被误判。
- 渲染结果中的每个新指令名都必须出现在调用方白名单中。

未知注解键会被丢弃并记录。默认通过 `console.warn` 记录，也可以传入
`logger`、`onUnknownAnnotation` 或 `report` 收集。非法值默认抛出
`AnnotationValidationError`；传入 `invalidValuePolicy: 'drop'` 可改为丢弃并记录。

## 可导入 API

核心导出：

- `renderAnnotations(annotations, templates, whitelistOrOptions): string`
- `renderAnnotationsDetailed(...)` / `buildAnnotationConfig(...)`: 返回 `config`、
  `dropped`（丢弃键）、`droppedDetails`（原因详情）、指令列表和状态机检查结果。
- `validateAnnotationValue(value, type): boolean`
- `validateConfig(config, whitelist): boolean`
- `inspectConfig(config, whitelist)`: 返回深度、错误、指令名等详情。
- `AnnotationRenderer`: 预绑定模板与选项的类。

运行测试：

```sh
npm test
```
