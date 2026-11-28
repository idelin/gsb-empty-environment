# 安全 XML 词法/语法解析与实体展开防御组件

面向企业 B2B 网关与文档转换服务的**零外部依赖** TypeScript 组件。输入为原始
`Uint8Array`，在任何 XML 处理之前完成编码审计、DTD/实体硬门禁与命名空间注入拦截，
用于阻断 XXE 文件读取、SSRF、Billion Laughs 内存爆炸与 XInclude/XSLT 外部资源加载。

运行时仅使用 ESM 与 Node 内置模块，无任何第三方依赖；通过 Node 原生 TypeScript
类型擦除（`--experimental-strip-types`）直接运行。

## 模块结构

| 文件 | 职责 |
| --- | --- |
| `src/decode.ts` | BOM 识别、字节序嗅探、UTF-8/UTF-16LE/UTF-16BE 严格解码、声明编码一致性审计 |
| `src/dtd.ts` | `<!DOCTYPE>` 与内部子集逐字符扫描，外部引用/参数实体硬门禁，内部通用实体提取 |
| `src/entity.ts` | 实体引用有向图环检测、帧栈式有界展开与实时配额熔断 |
| `src/scanner.ts` | 命名空间栈、元素深度、XInclude/XSLT 注入拦截、文档级实体校验 |
| `src/xml-lex.ts` | XML 名称/空白/注释/PI 词法工具 |
| `src/errors.ts` | 统一错误类型 `XmlSecurityError` 与稳定错误码 |
| `src/index.ts` | 公共导出入口 |

## 快速使用

```ts
import { inspectXml, safeInspectXml } from './src/index.ts';

const bytes: Uint8Array = receiveRawSoapMessage();

// 1) 抛异常风格：任何防御性中止都抛出 XmlSecurityError
try {
  const result = inspectXml(bytes, { dtdMode: 'strict' });
  console.log(result.encoding, result.elementCount);
} catch (error) {
  // error.code 为稳定机器可读错误码，例如 'XINCLUDE_BLOCKED'
}

// 2) 不抛异常风格：通过返回 XmlSecurityError 对象表达拒绝
const outcome = safeInspectXml(bytes, { dtdMode: 'internal-entities' });
if (outcome instanceof XmlSecurityError) {
  rejectMessage(outcome.code);
}
```

## 配置项

```ts
inspectXml(bytes, {
  dtdMode: 'strict',             // 'strict'（零 DTD）或 'internal-entities'
  maxInputBytes: 10 * 1024 * 1024,
  maxElementDepth: 1000,
  quotas: {
    maxTotalExpandedChars: 1_000_000, // 文档级引用产出字符总配额
    maxExpansionMultiplier: 100,       // 单实体相对叶子基准长度的最大放大倍数
    maxExpansionDepth: 10,             // 实体引用最大嵌套深度
  },
});
```

## 防御矩阵

### 1. 编码与声明审计

- 识别 UTF-8/UTF-16LE/UTF-16BE BOM；无 BOM 时按 XML 附录 F 四字节模型嗅探字节序。
- 严格解码：拒绝超长 UTF-8、孤立代理、非法续字节、奇数长度 UTF-16、孤立代理项。
- 解析 `<?xml ... encoding="..."?>`，仅放行白名单编码别名（UTF-8、UTF-16LE/BE 等），
  UTF-7、ISO-8859-1 等一律 `ENCODING_NOT_WHITELISTED`。
- 声明编码与 BOM/嗅探结果不一致时 `ENCODING_MISMATCH`，杜绝编码伪装。

### 2. DTD 与实体硬门禁

- `strict`：任何 `<!DOCTYPE`（含大小写变体）直接 `DTD_STRICT_MODE`。
- `internal-entities`：
  - 全部 `SYSTEM`/`PUBLIC` 外部 DTD 与外部实体 `DTD_EXTERNAL_REFERENCE`；
  - 全部参数实体声明与 `%name;` 引用（含实体值内）`DTD_PARAMETER_ENTITY`；
  - `ELEMENT`/`ATTLIST`/`NOTATION` 等非实体构造 `DTD_UNSUPPORTED_CONSTRUCT`；
  - 仅允许引号包裹的内部通用实体。
- 解析期对实体引用关系做 DFS 有向图环检测，循环实体在任何展开前 `ENTITY_RECURSION`。
- 展开采用帧栈逐段写入：每段字面量在写入**之前**检查帧预算与文档总预算，
  绝不“先展开再测长”；超限立即中止（总配额/倍数/深度三类错误码）。

### 3. 命名空间感知注入拦截

- 维护完整前缀映射栈（含默认命名空间），不依赖固定前缀名。
- 任意绑定到 `http://www.w3.org/2001/XInclude` 的 `include` 元素（如 `foo:include`）
  一律 `XINCLUDE_BLOCKED`。
- 任意绑定到 `http://www.w3.org/1999/XSL/Transform` 的 `import`/`include` 元素
  `XSLT_IMPORT_BLOCKED`；其属性值中的 `document()` 调用（大小写/空白不敏感）
  `XSLT_DOCUMENT_BLOCKED`。
- 元素嵌套深度超 `maxElementDepth` 时 `ELEMENT_DEPTH_EXCEEDED`。

## 错误码

`ENCODING_MALFORMED`、`ENCODING_NOT_WHITELISTED`、`ENCODING_MISMATCH`、
`DECLARATION_MALFORMED`、`INPUT_TOO_LARGE`、`DTD_STRICT_MODE`、
`DTD_EXTERNAL_REFERENCE`、`DTD_PARAMETER_ENTITY`、`DTD_UNSUPPORTED_CONSTRUCT`、
`DTD_MALFORMED`、`ENTITY_RECURSION`、`ENTITY_EXPANSION_TOTAL_EXCEEDED`、
`ENTITY_EXPANSION_MULTIPLIER_EXCEEDED`、`ENTITY_EXPANSION_DEPTH_EXCEEDED`、
`ENTITY_UNDECLARED`、`CHARACTER_REFERENCE_INVALID`、`NAMESPACE_PREFIX_UNDECLARED`、
`XINCLUDE_BLOCKED`、`XSLT_IMPORT_BLOCKED`、`XSLT_DOCUMENT_BLOCKED`、
`ELEMENT_DEPTH_EXCEEDED`、`XML_MALFORMED`。

## 测试

```bash
npm test
```

覆盖 UTF-16 伪装 XXE、参数实体外带、10 层十亿笑弹（文本与属性两条通道）、
重命名前缀 `foo:include` XInclude 绕过、XSLT `import`/`include`/`document()`、
环检测、深度/倍数/总配额熔断、畸形字节与合法 XML 等 33 个用例。
