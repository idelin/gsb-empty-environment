# Taint Propagation Toolkit

TypeScript 污点传播包：为「脚本检查服务」提供 Perl 污点模式（taint mode）
风格的运行时污点跟踪。外部输入被标记为不可信，值在赋值、拼接、插值、
join、substr、哈希与数组元素之间传播；只有满足严格条件的正则捕获才能清洗
污点；污点流入 `system`、`exec`、反引号或写模式 `open` 时抛出携带完整
来源链的违规异常。

零依赖，基于 Node 内置 TypeScript 类型擦除（`--experimental-strip-types`）
与 `node:test` 运行。

## 传播规则

| 操作 | API | 污点传播 |
| --- | --- | --- |
| 外部输入 | `host.source(name, raw)` | 登记 origin 事实，值带污点 |
| 赋值 | `host.assign(v)` | 复制右值污点，挂 `assign` 事实 |
| 拼接 | `host.concat(a, b)` | 任一操作数污点则结果污点，父事实全部保留 |
| 插值 | `` host.interpolate`x=${v}` `` | 任一插值位污点则整串污点 |
| join | `host.join(sep, arr)` | 任一元素或分隔符污点则结果污点 |
| substr | `host.substr(v, off, len?)` | 子串沿用原串污点（不清洗） |
| 数组元素 | `TArray.push/set/get` | 元素各自携带右值污点 |
| 哈希值 | `THash.set/get` | 值沿用存入时的污点；污点键访问再挂 `hash-get` 事实 |
| 正则捕获 | `host.capture(v, re)` | 见下节，默认沿用污点 |

## 捕获清洗判定

`host.capture` 仅在同时满足以下条件时把捕获组清洗为干净值（白名单语义）：

1. 模式源码以 `^` 开头并以 `$` 结尾（整串锚定）；
2. 模式源码不含 `.*`（防止 `.*` 吞掉任意恶意内容）；
3. 模式不带 `m` 标志（否则 `^`/`$` 退化为行边界）。

其余一切捕获（未锚定、含 `.*`、带 `m` 标志）都沿用输入污点，并在事实图
上记录 `capture` 事实及拒绝原因（`not-whole-string-anchored` /
`pattern-contains-.*` / `anchor-defeated-by-m-flag`）。判定函数
`anchoringVerdict(re)` 可单独调用。

## 危险宿主与来源链

`SinkHost` 模拟四类危险操作，任何污点参数流入都会抛出 `TaintViolation`：

- `system(cmd)`、`exec(cmd)`、`backtick(cmd)`：命令执行与反引号捕获；
- `open(expr)`：`>`、`>>`、`|`、`|-` 写/管道模式；读模式 `<` 不管控。

`TaintViolation` 携带：

- `sink`：危险操作类型；
- `value`：触发违规的污点值；
- `chain`：主来源链（逐步操作标签、事实 id、父事实 id）；
- `origins`：事实图可达的全部污点根来源（多来源拼接时不止一个）；
- `value.chainString()`：人类可读的 `->` 串联来源链。

## 脚本检查服务门面

`InspectionService` 是供检查服务调用的统一入口：

```ts
import { InspectionService } from "./src/index.ts";

const svc = new InspectionService();
const input = svc.host.source("cgi-param", "alice; rm -rf /");

// 只检查不抛异常
svc.isTainted(input);                 // true
svc.report(input).origins[0].source;  // "cgi-param"
svc.chainString(input);               // "origin(cgi-param=...)"

// 委托危险宿主：污点即抛 TaintViolation
svc.guard("system", input);
```

## 目录结构

- `src/tval.ts`：污点事实图（origin/link DAG）、`TVal` 与来源链渲染；
- `src/collections.ts`：`TArray`、`THash` 的元素/键值传播；
- `src/host.ts`：传播算子与 `anchoringVerdict` 清洗判定；
- `src/sinks.ts`：`SinkHost`、`TaintViolation`、写模式识别；
- `src/inspection.ts`：检查服务门面；
- `test/taint.test.ts`：16 个用例，覆盖题目要求的四个验证场景。

## 运行

```sh
npm test          # node --experimental-strip-types --test test/taint.test.ts
node --experimental-strip-types demo.ts   # 端到端来源链演示
```
