# Perl 污点传播包

供脚本检查服务调用的 TypeScript 污点分析包。它把 Perl 外部输入标记为污点，
跟踪赋值、拼接、插值、`join`、`substr`、哈希元素和数组元素，并在污点到达
`system`、`exec`、反引号或写模式 `open` 时输出完整来源链。

## 快速使用

```ts
import { analyzePerl } from "./index.ts";

const result = analyzePerl(
  `
  my $input = <STDIN>;
  $input =~ /(\\w+)/;
  my $cmd = "echo $1";
  system($cmd);
  `,
  { file: "check.pl" },
);

for (const finding of result.findings) {
  console.log(finding.sink, finding.chain[0]);
}
```

来源链示例：

```text
<STDIN> (stdin-input, check.pl:2:15)
  -> $input (assignment, check.pl:2:13)
  -> regex capture $1 (regex-capture-retained, check.pl:3:10)
  -> string interpolation (interpolation, check.pl:4:13)
  -> $cmd (assignment, check.pl:4:11)
  -> system (sink, check.pl:5:3)
```

`analyzePerl` 返回：

- `findings`：每个汇点一条记录，包含 `sink`、位置、表达式、`traces`、
  `sources` 和可直接展示的 `chain` 字符串。
- `sources`：本次分析命中的去重来源节点。
- `variables`：分析结束时的变量/容器污点快照。

`analyze`、`analyzePerlScript`、`scanPerlTaint` 和 `checkPerlScript` 是同一入口的别名。

## 传播与清洗规则

- 赋值、`.=`、拼接、双引号/`qq` 插值、`join`、`substr` 都保留污点。
- 哈希按键精确跟踪：`$h{cmd}` 优先使用该键的值，未知键回退到 `%h` 的聚合污点。
- 数组按下标精确跟踪；`push`/`unshift` 会污染数组聚合值，未知下标回退到聚合值。
- 外部来源包括 `$ARGV`、`@ARGV`、`$ENV`、`%ENV`、`<STDIN>`、`<>`、
  `param(...)`、`CGI::param(...)`、`readline(...)`、`shift`。
- 正则捕获 `$1`、`$2` 等只有在匹配整个字符串、模式以 `^` 开头并以未转义 `$`
  结尾、且模式不含 `.*` 时才清洗；`/m` 模式不满足整串锚定。
- 未满足清洗条件的捕获继承原输入污点，并记录 `regex-capture-retained` 步骤。
- 整体匹配结果 `$&`、匹配前文本 `` $` ``、匹配后文本 `$'` 始终保留污点。
- 反引号和 `qx` 命令的插值内容直接作为 `backtick` 汇点检查。
- `open` 仅在 `>`、`>>`、`+>`、`>|` 等写模式（或模式本身受污染）时报告。
  三参数 `open(FH, ">", $path)` 会检查路径参数。

## 底层传播 API

`src/taint.ts` 也可被宿主直接调用：

```ts
import {
  assignTaint,
  concatTaint,
  interpolateTaint,
  joinTaint,
  substrTaint,
  hashValueTaint,
  arrayElementTaint,
  regexCaptureTaint,
  taintSource,
} from "./index.ts";

const input = taintSource("<STDIN>");
const captured = regexCaptureTaint(input, "(\\w+)", 1);
const command = concatTaint([captured], { label: "$cmd" });
const hashValue = hashValueTaint(command, "cmd");
const arrayValue = arrayElementTaint(hashValue, 0);
const assigned = assignTaint(arrayValue, { label: "$final" });
```

`TaintValue` 形如 `{ tainted, traces }`，每条 trace 保存
`{ origin, steps }`。`describeTaintTrace` 和 `formatTaintChain` 可用于渲染来源链。

## 验证

```sh
npm test
```

测试覆盖未锚定捕获、`.*`、锚定白名单捕获、拼接回污点、插值、`join`、
`substr`、哈希值、数组元素、反引号和写模式 `open`。

## 边界

分析器是面向检查场景的轻量 Perl 前端，不做完整 Perl 语法或跨子程序数据流分析。
对无法静态确定的哈希键、数组下标和函数参数采用保守合并，宁可保留污点也不静默清洗。
