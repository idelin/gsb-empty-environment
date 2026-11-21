# 下载响应类型防护组件（Download Type Guard）

基于声明 MIME 类型的下载响应防护，默认失败关闭（fail closed），运行于 Node 内置 test runner。

## 防护规则

1. **禁止嗅探头部**：每个响应（含多段响应的每个 part）都附加 `X-Content-Type-Options: nosniff`。
2. **强制附件下载**：可解释类型（HTML、XHTML、SVG、XML、CSS、JavaScript、PDF）与可执行类型
   （PE/ELF/Mach-O、脚本、class、`application/octet-stream` 等）一律输出
   `Content-Disposition: attachment`；惰性安全类型才允许 `inline`。处置仅由声明类型推导，
   请求头与查询参数无法影响。
3. **扩展名核对**：文件名扩展名必须在声明类型的允许集合内，且扩展名反向暗示的 MIME 必须与声明一致；
   无文件名、无扩展名或未知声明类型直接拒绝。
4. **内容嗅探核对**：通过 magic bytes 嗅探实际类型，与声明类型冲突时拒绝（zip 同族容器与
   `octet-stream` 承载二进制有明确豁免规则）。
5. **内容协商**：同一资源的所有表示（representation）必须声明同一 MIME 类型；协商成功时附加
   `Vary: Accept`。`Accept` 只能选择表示，不能改变声明类型。
6. **请求覆盖拦截**：`type`/`format`/`response-content-type` 等查询参数以及请求头若试图改写声明
   类型或处置（`disposition`/`inline`/`download` 等），按协商绕过拒绝；与现有决策一致的值忽略。
7. **多段响应**：`guardMultipart` 逐段独立防护；`sameResource` 模式下各段声明类型必须一致。

## API

```ts
import { guardResponse, guardNegotiated, guardMultipart } from "./src/index.ts";

// 单个下载响应
guardResponse(
  { declaredType: "image/png", filename: "pic.png", body: bytes, headers: upstreamHeaders },
  { query: req.query, headers: req.headers },
);

// 内容协商（同一资源的多个表示，类型必须一致）
guardNegotiated([rep1, rep2], requestContext);

// 多段响应（sameResource 时各段类型必须一致）
guardMultipart([{ name: "a", ...rep }], requestContext, { sameResource: true });
```

## 错误模型

所有拒绝都抛出带 `code` 的 `TypeGuardError`：

- `extension-mismatch`：错配 1，扩展名与声明类型不一致。
- `content-sniff-mismatch`：错配 2，magic bytes 与声明类型不一致。
- `disposition-mismatch`：错配 3，上游处置与类型推导结果冲突。
- `negotiation-bypass`：协商/多段绕过，包括表示类型不一致、查询参数或请求头改写类型/处置、
  上游 `Content-Type` 冲突。

## 验证

```sh
npm test
```

测试分三组：`test/mismatch.test.ts`（三类错配）、`test/negotiation-bypass.test.ts`
（协商与多段绕过）、`test/guard.test.ts`（正向基线）。
