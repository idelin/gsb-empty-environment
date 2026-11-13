# 笔记本输出信任标记与签名重算

数据分析平台打开别人发来的笔记本文件时，只有被信任的笔记本才允许渲染输出里的 HTML 与脚本。`index.ts` 是可导入的 TypeScript 信任判定包，不依赖第三方库。

## 安全规则

- 签名是 HMAC-SHA256，密钥为服务器密钥。
- HMAC 输入是整个笔记本的规范化 JSON：递归按键名字典序排序、不带空白，并在计算前去掉顶层 `metadata.signature`。
- 签名覆盖单元代码、元数据和所有单元的全部输出，因此改动代码或输出都会使旧签名失效。
- 打开时只使用重算出的签名查询已登记集合；文件内嵌的 `metadata.signature` 不参与信任判断，也不会被登记。
- 不信任的笔记本会移除 `text/html`、`application/javascript`、`image/svg+xml` 输出，并移除输出 `metadata.trusted`，不读取其中的任何信任声明。
- 只有用户在界面执行并保存时，`saveNotebook` 才重算并登记新签名。

## 基本用法

```ts
import {
  NotebookTrustService,
  openNotebook,
  saveNotebook,
} from "./index.ts";

const serverSecret = process.env.NOTEBOOK_SIGNING_KEY!;
const registeredSignatures = new Set<string>();

// 打开别人发来的文件：重算签名，未登记则不信任并净化输出。
const opened = openNotebook(importedJson, serverSecret, registeredSignatures);
if (!opened.trusted) {
  const safeNotebook = opened.notebook;
}

// 用户在界面执行并保存：只登记这次重算出的签名。
const saved = saveNotebook(executedNotebook, serverSecret, registeredSignatures);
// saved.notebook.metadata.signature === saved.signature
```

也可以使用有状态的服务：

```ts
const service = new NotebookTrustService(serverSecret, registeredSignatures);
const opened = service.open(importedJson);
const saved = service.save(executedNotebook);
```

`secret` 可以是字符串或 `Uint8Array`。已登记签名集合可以是 `Set`、数组或其他字符串迭代器；传入可变 `Set` 时，`saveNotebook` 会原地登记。

## 导出 API

- `canonicalizeNotebook(notebook)` / `canonicalNotebookJSON`：返回用于 HMAC 的规范化 JSON。
- `canonicalStringify(value)` / `stableStringify`：通用稳定 JSON 序列化。
- `computeNotebookSignature(notebook, secret, encoding?)` / `signNotebook`：默认返回 64 位十六进制 HMAC。
- `isNotebookTrusted(notebook, secret, signatures)` / `isTrustedNotebook`：重新计算并与登记集合比较。
- `openNotebook(notebook, secret, signatures)` / `loadNotebook`：返回 `{ notebook, trusted, signature, removedOutputs, removedMimeTypes, ... }`。
- `saveNotebook(notebook, secret, signatures)` / `saveAndRegisterNotebook`：重算签名、写入 `metadata.signature` 并登记。
- `sanitizeNotebookOutputs(notebook)` / `sanitizeNotebook`：只做危险 MIME 与 `metadata.trusted` 清理。
- `createNotebookTrustService(secret, signatures?)` / `NotebookTrustService`：持有密钥与签名集合的服务封装。

`openNotebook`、`saveNotebook` 的第二个参数也可传 `{ secret, signatures }` 选项对象。

## 测试

```bash
npm test
```

测试覆盖伪造 `trusted`、保存后代码/输出篡改、键序不同但内容相同、导入文件自带签名以及纯文本输出保留。
