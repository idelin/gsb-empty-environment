# 多租户网关增量解码包

同一授权对象的 JSON 文本入口与二进制定长整数入口共享同一套 schema 校验、
身份树与签名键逻辑，保证两条路径在任意字节切点下产生完全一致的
`minimal` 编码与 `signatureKey`。

## 模块

- `src/types.ts`：类型图、迁移、错误码与结果的公共类型。
- `src/schema.ts`：类型图编译、字段编号/别名索引、稳定摘要 `digest`。
- `src/lexer.ts`：字节级 JSON 分词器，增量 UTF-8 解码、数字词法原样保留。
- `src/lex64.ts`：id64 词法校验（拒绝前导零、科学计数法、`-0`、超范围）。
- `src/json.ts`：流式 JSON 解码器、别名冲突、迁移捕获与检查点。
- `src/binary.ts`：网络序二进制流式解码器、未知字段跳过与检查点。
- `src/identity.ts`：身份树、按字段编号排序的最小编码与签名键。
- `src/encode-binary.ts`：测试/出站侧二进制编码器。
- `src/reference.ts`：小对象整量参考解码。
- `src/index.ts`：网关导入门面（`decodeJson` / `decodeBinary` / 流式类）。

## 核心约定

- 字段身份只认声明编号 `FieldDef.no`；属性名与别名仅用于 JSON 查表。
- JSON 数字保留原始词法，直到与字段类型比对；id64 字符串可承载超过
  `Number.MAX_SAFE_INTEGER` 的精确值，数字词法必须是精确安全整数。
- 两个别名映射到同一字段编号时，无论值是否相同都报 `DUPLICATE_FIELD`。
- 未知非关键字段被语法完整跳过，字节绝不进入授权身份；`strictUnknown`
   可改为直接报错。
- 签名键绑定租户、类型版本、根类型编号与按编号排序的最小编码；
  授权回调只在全部输入校验通过后触发。
- 检查点绑定 schema 摘要、解析深度、每个未闭合对象的已见字段集合与
  完整词法/帧状态，可跨任意字节切块恢复；换版类型图因 digest 不同被拒绝。

## 二进制线格式（大端）

```
message := u32 typeNo | u32 fieldCount | field*
field   := u32 fieldNo | u32 length | value(length 字节)
array   := u32 count | element*，element := u32 length | value
```

标量值：bool 1 字节，int32 4 字节有符号，id64 8 字节有符号，
string 为 UTF-8；对象 value 是内嵌 message。

## 运行测试

```
npm test
```

测试覆盖 UTF-8/代理对边界、不同深度同编号、超安全整数相等误判、
多段别名冲突、未知字段吞邻值、迁移、检查点换版以及全部字节切点恢复。
