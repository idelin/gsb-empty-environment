# json-columnar-projector

结构化 JSON 扁平化列式投影器（TypeScript 实现，零运行时依赖）。

- **动态拆解**：自动推断 JSON 字段路径，把相同路径的值抽取到独立虚拟列（`flatten`）。
- **按需投影**：查询只指定少数叶子路径时，基于原始文本的流式扫描仅物化相关虚拟列；
  无关子树（如订单的商品品列表）通过纯指针扫描整体跳过，**不触发 `JSON.parse`、不构造中间对象**（`project`）。

## 路径语法

| 语法 | 含义 |
| --- | --- |
| `user.address.city` | 对象字段逐级下钻 |
| `items[].sku` | 数组通配：对每个元素取 `sku`，同一记录内多值折叠为数组单元 |
| `items[0].price` | 数组定向下标 |

根节点为对象时视为单条记录（1 行）；根节点为数组时每个元素是一条记录，列按行号对齐，缺失路径补 `undefined`。

## API

```ts
import { flatten, project, projectValue, ColumnStore } from './src';

// 全量扁平化：自动推断所有叶子路径 -> 虚拟列
const { store } = flatten(jsonText);
store.paths();                        // ['channel', 'items[].sku', 'user.address.city', ...]
store.getColumn('user.address.city'); // ['上海市']

// 按需投影：只物化指定路径，其余子树整体跳过
const { store: cols, stats } = project(jsonText, ['orderId', 'user.address.city']);
stats.skippedBytes;                   // 被跳过的无关字节数（未反序列化）
```

## 构建 / 测试 / 示例

```bash
npm run build    # 通过 TypeScript 编译器 API 编译到 dist/（离线环境见下）
npm test         # 构建 + 运行 node:test 测试
npm run example  # 构建 + 运行示例
```

> 离线环境说明：本仓库不依赖 npm 安装。`scripts/build.mjs` 依次尝试本地
> `node_modules/typescript`、`TYPESCRIPT_PATH` 环境变量、本机 Cursor 内置 TypeScript。
> 测试文件按 Node 内置 `node:test` 标准 API 编写（Node >= 18 直接运行）；
> 旧版本 Node 自动降级到 `test/fallback-runner.ts` 兼容执行器。

## 运行样例（`npm run example`）

输入为含 502 行商品的复杂嵌套订单 JSON（86,849 字符）：

```
=== flatten: 自动推断的叶子路径（前 12 条） ===
  channel
  ext
  items[].attrs.color
  items[].attrs.size
  items[].attrs.switch
  items[].price
  items[].qty
  items[].sku
  items[].title
  logistics.carrier
  logistics.routes[].at
  logistics.routes[].node
  ... 共 34 个虚拟列

=== project: 仅投影 'orderId' 与 'user.address.city' ===
物化的虚拟列: [ 'orderId', 'user.address.city' ]
orderId           -> [ 'SO-20261003-0001' ]
user.address.city -> [ '上海市' ]

=== 投影统计（证明未反序列化无关结构） ===
  总字节数        : 86849
  跳过字节数      : 86506 (99.6%)
  跳过子树数      : 16
  物化叶子值个数  : 2
```

## 测试结果（`npm test`，12 项全部通过）

```
ok 1 - flatten: 自动推断字段路径并抽取虚拟列
ok 2 - project: 按需投影，只物化相关虚拟列
ok 3 - 推断嵌套订单的全部叶子路径
ok 4 - 按 'user.address.city' 建立独立列存切片
ok 5 - 仅请求城市维度时只产生对应列
ok 6 - 无关结构（商品列表等）被整体跳过，不做反序列化
ok 7 - 投影过程不调用 JSON.parse（无整文档反序列化）
ok 8 - 投影结果与全量 JSON.parse 引用一致
ok 9 - 请求子树路径时物化该子树（仅对命中子串调用 JSON.parse）
ok 10 - 根数组：多记录按行对齐，缺失路径补 undefined
ok 11 - 跳过含引号/括号/转义的无关子树不误判边界
ok 12 - 非法 JSON 抛出带偏移量的语法错误

# tests 12
# pass 12
# fail 0
```

## 目录结构

```
src/
  scanner.ts        原始 JSON 文本扫描器（skipValue 纯指针跳过子树）
  path.ts           路径解析（. / [] / [n]）与前缀树匹配
  column-store.ts   列式存储：路径 -> 按行对齐的虚拟列
  projector.ts      flatten / project / projectValue
test/
  projector.test.ts node:test 测试（12 项）
  harness.ts        node:test 标准 API 封装（旧 Node 自动降级）
  fixture.ts        复杂嵌套业务订单样例
examples/demo.ts    运行样例
scripts/build.mjs   离线 TypeScript 构建脚本
```
