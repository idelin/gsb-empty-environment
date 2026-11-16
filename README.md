# 反向代理配置片段注入与指令白名单

入口控制器会把用户写在资源上的注解值拼进反向代理的配置文件，攻击者在值里放换行、分号和右花括号，提前结束当前块并注入 load_module 之类的指令，在代理进程里执行任意代码。为TypeScript入口控制器写可导入的注解渲染包，输入注解键值和目标指令模板：值按模板声明的类型校验，token 类型只允许 [A-Za-z0-9._-]，size 类型匹配数字加可选 k 或 m 后缀，path 类型必须以 / 开头且不含 .. 段；任何类型的值都不得含换行、回车、NUL、分号、花括号、反引号、$ 和以 # 开头的内容；渲染后的整份配置用括号与分号的状态机复核，块深度必须回到 0，新增的指令名必须在调用方白名单内；未知注解键丢弃并记录。用换行加分号、右花括号提前闭合、$ 变量引用、.. 路径和合法 size 验证。

## 用法

```ts
import { renderAnnotations } from "./index.ts";

const result = renderAnnotations(
  { "proxy/body-size": "10m", "proxy/upstream": "/api/v2", "evil/x": "1; }" },
  {
    templates: {
      "proxy/body-size": { directive: "client_max_body_size", type: "size" },
      "proxy/upstream": { directive: "proxy_pass", type: "path", block: "location /api" },
    },
    allowedDirectives: ["client_max_body_size", "proxy_pass"],
    baseConfig: "http {\n  server {\n    listen 80;\n  }\n}",
  },
);

if (!result.ok) {
  // result.config 为 null，诊断信息见 result.diagnostics
}
```

## 导出

- `renderAnnotations(annotations, options)`：渲染并做全部复核，返回 `fragment`、`config`、`ok`、`diagnostics`。
- `validateToken` / `validateSize` / `validatePath`：单值类型校验。
- `verifyConfigStructure(config)`：括号与分号状态机，块深度必须回到 0。
- `verifyDirectiveWhitelist(directives, allowed)`：指令名白名单复核。

## 安全保证

- 危险字符（换行、回车、NUL、`;`、`{}`、反引号、`$`）与 `#` 开头内容对所有类型一律拒绝，并逐条记录诊断码。
- 模板声明的 `token` / `size` / `path` 类型各自再做格式校验，`path` 拒绝任何 `..` 段。
- 未知注解键只产生 `warning` 并丢弃，绝不参与渲染。
- 任一校验失败时 `config` 返回 `null`，避免半成品配置被写入代理进程。

## 测试

`npm test`（基于 `node --experimental-strip-types --test`，覆盖换行分号注入、右花括号提前闭合、`$` 变量引用、`..` 路径与合法 size 等用例）。
