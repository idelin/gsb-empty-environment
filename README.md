# 上下文感知模板转义组件

按变量输出时所处的 HTML 解析上下文选择独立编码规则，覆盖六类上下文，
并在脚本上下文中拒绝未编码的原始输出。

## 上下文与编码矩阵

| 上下文 | 触发位置 | 编码规则 | 原始输出 `{{{ }}}` |
| --- | --- | --- | --- |
| `html` | 元素正文 | 实体感知转义 `& < >` | 允许 |
| `attr` | 普通属性值 | 额外转义引号、反引号、空白 | 允许 |
| `js` | `<script>` 与 `on*` 事件属性 | JSON 字面量，转义 `< >` 与 U+2028/2029 | 拒绝，抛 `UnsafeRawOutputError` |
| `url` | `href/src/action/...` | 协议白名单校验 + 属性安全编码 | 允许（仍校验协议） |
| `comment` | `<!-- ... -->` | `- < > " '` 等实体化，阻止 `-->` 闭合 | 允许 |
| `css` | `<style>` | 白名单字符外一律 CSS 十六进制转义 | 允许 |

URL 协议白名单：`http/https/mailto/tel/ftp` 与相对地址；`javascript:`、
`vbscript:`、`data:` 等直接抛 `UnsafeUrlError`。校验前会剥离控制字符并
解码实体，拦截 `java\nscript:`、`&#106;avascript:` 等混淆绕过。

## 幂等（双重编码安全）

- `escape()` 始终返回 `SafeString`，再次进入 `escape()` 时原样透传，
  六类上下文重复转义结果不变。
- `html/attr/comment` 的底层编码器为实体感知：识别已存在的命名/数值实体，
  对裸字符串再次编码也不会把 `&lt;` 变成 `&amp;lt;`。

## 模板语法

```ts
import { render } from './src/index.ts';

render('<div>{{ name }}</div>', { name: '<b>x</b>' });
// <div>&lt;b&gt;x&lt;/b&gt;</div>

render('<script>var config = {{ config }};</script>', {
  config: { host: 'a"b</script>' },
});
// 输出合法 JSON 字面量，引号与 </script> 均被转义

render('<a href="{{ url }}">go</a>', { url: 'javascript:alert(1)' });
// 抛 UnsafeUrlError

render('<script>var x = {{{ x }}};</script>', { x: '1' });
// 抛 UnsafeRawOutputError
```

## 结构

- `src/escaper.ts`：六类上下文编码器、`SafeString`、协议校验、实体解码
- `src/engine.ts`：单遍状态机模板引擎，自动识别插值所在上下文
- `src/index.ts`：统一导出
- `test/escaper.test.ts`：编码器单元测试
- `test/engine.test.ts`：六类注入、脚本原始输出拒绝、URL 属性、双重编码幂等等集成测试

## 运行测试

```sh
node --experimental-strip-types --test 'test/**/*.test.ts'
```
