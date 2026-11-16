# 笔记本输出信任标记与签名重算

数据分析平台打开别人发来的笔记本文件时，只有被信任的笔记本才允许渲染输出里的 HTML 与脚本，攻击者在输出元数据里写 trusted: true，或在改动单元代码后保留旧签名。给TypeScript笔记本服务实现可导入的信任判定包，输入笔记本 JSON、服务器密钥和已登记的签名集合：信任签名为 HMAC-SHA256，输入是去掉 metadata.signature 字段、键按字典序排序、不带空白的规范化 JSON，且覆盖所有单元的输出；打开时重算签名，在已登记集合中才信任；不信任时丢弃所有 text/html、application/javascript、image/svg+xml 输出，并忽略输出元数据里的 trusted 字段；用户在界面执行并保存后，以重算结果登记新签名；导入文件里自带的 signature 字段一律忽略，不得因此登记。用伪造 trusted 字段、保存前后内容变更、键序不同但内容相同、自带签名和纯文本输出验证。

## 信任规则

- 签名算法：`HMAC-SHA256`，输出为十六进制摘要，密钥由服务端持有。
- 规范化负载：深拷贝后删除根级 `metadata.signature`，所有对象键递归按字典序排序（数组保持原序），再以无空白 `JSON.stringify` 序列化；负载覆盖全部单元及其输出。
- 打开（`openNotebook`）：始终重算签名，仅当结果存在于已登记集合时 `trusted: true`；输出元数据里的 `trusted` 与文件自带的 `metadata.signature` 都不参与判定。
- 不信任时：删除每个输出 `data` 中的 `text/html`、`application/javascript`、`image/svg+xml`，并删除输出 `metadata.trusted`；只剩危险 MIME 的富输出整体丢弃，`stream`、`error` 与纯文本输出保留。
- 保存（`saveNotebook` / `registerSignature`）：对当前内容重算签名并登记；文件自带的 signature 永不登记，也不会写入返回的笔记本。

## 导出 API（`index.ts` / `src/trust.ts`）

- `computeSignature(notebook, serverKey): string`
- `canonicalizeNotebook(notebook): string`
- `isNotebookTrusted(notebook, serverKey, registeredSignatures): boolean`
- `openNotebook(notebook, serverKey, registeredSignatures): { trusted, notebook, signature }`
- `sanitizeUntrustedNotebook(notebook): Notebook`
- `saveNotebook(notebook, serverKey, registeredSignatures): { signature, notebook }`
- `registerSignature(notebook, serverKey, registeredSignatures): string`

## 运行测试

```sh
node --experimental-strip-types --test
```
