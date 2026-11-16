/**
 * 安全的注解 -> 反向代理配置渲染包。
 *
 * 设计目标：用户可控的注解值永远不能突破模板声明的指令边界。
 *  - 每个值先经过与类型无关的“危险字符”检查，再按模板声明的类型校验。
 *  - 渲染出的整份配置用括号 / 分号状态机复核，块深度必须回到 0。
 *  - 模板里出现的指令名必须落在调用方提供的白名单内。
 *  - 未知注解键直接丢弃，并以 warning 形式记录，绝不参与渲染。
 */

/** 模板可声明的值类型。 */
export type AnnotationType = "token" | "size" | "path";

/** 一条目标指令模板，描述某个注解键如何渲染成配置指令。 */
export interface DirectiveTemplate {
  /** nginx 风格的指令名，例如 client_max_body_size。 */
  directive: string;
  /** 值的校验类型。 */
  type: AnnotationType;
  /**
   * 可选的块头（例如 location /）。提供后指令渲染进 `header { ... }` 块，
   * 不提供则渲染在顶层。相同块头的指令会合并到同一个块内。
   */
  block?: string;
}

/** 注解键 -> 目标指令模板。 */
export type DirectiveTemplates = Record<string, DirectiveTemplate>;

/** 用户在资源上写的原始注解键值。 */
export type Annotations = Record<string, string>;

export interface RenderOptions {
  /** 注解键 -> 模板映射。 */
  templates: DirectiveTemplates;
  /** 调用方允许出现在最终配置里的指令名白名单。 */
  allowedDirectives: readonly string[];
  /** 可选的既有配置骨架，渲染结果会拼接在其后一起复核。 */
  baseConfig?: string;
}

export type DiagnosticLevel = "error" | "warning";

export interface Diagnostic {
  level: DiagnosticLevel;
  code: string;
  message: string;
  key?: string;
  value?: string;
  directive?: string;
}

export interface RenderResult {
  /** 仅渲染新增指令的片段（不含 baseConfig）。 */
  fragment: string;
  /** 拼接 baseConfig 后并通过全部复核的整份配置；失败时为 null。 */
  config: string | null;
  ok: boolean;
  diagnostics: Diagnostic[];
}

export interface StateMachineResult {
  ok: boolean;
  depth: number;
  diagnostics: Diagnostic[];
}

/** 任何类型的值都禁止出现的字符。 */
const FORBIDDEN_CHARS: ReadonlyArray<{ ch: string; code: string; name: string }> = [
  { ch: "\n", code: "NEWLINE", name: "换行" },
  { ch: "\r", code: "CARRIAGE_RETURN", name: "回车" },
  { ch: "\0", code: "NUL", name: "NUL" },
  { ch: ";", code: "SEMICOLON", name: "分号" },
  { ch: "{", code: "OPEN_BRACE", name: "左花括号" },
  { ch: "}", code: "CLOSE_BRACE", name: "右花括号" },
  { ch: "`", code: "BACKTICK", name: "反引号" },
  { ch: "$", code: "DOLLAR", name: "$ 变量引用" },
];

const TOKEN_RE = /^[A-Za-z0-9._-]+$/;
const SIZE_RE = /^\d+[km]?$/;
/** 指令名 / 块头标识符允许的字符（杜绝模板自身夹带分隔符）。 */
const NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;

function forbiddenDiagnostics(value: string): Diagnostic[] {
  const diagnostics: Diagnostic[] = [];
  for (const entry of FORBIDDEN_CHARS) {
    if (value.includes(entry.ch)) {
      diagnostics.push({
        level: "error",
        code: `FORBIDDEN_${entry.code}`,
        message: `值中不允许出现${entry.name}字符`,
        value,
      });
    }
  }
  return diagnostics;
}

function startsWithHash(value: string): boolean {
  // 去掉前置的空格 / 制表符后，若以 # 开头则视为注释注入（防御前置空白）。
  return /^[ \t]*#/.test(value);
}

/** token：只允许 [A-Za-z0-9._-]。 */
export function validateToken(value: string): boolean {
  return TOKEN_RE.test(value);
}

/** size：数字加可选的 k 或 m 后缀。 */
export function validateSize(value: string): boolean {
  return SIZE_RE.test(value);
}

/** path：必须以 / 开头，且不含 .. 段。 */
export function validatePath(value: string): boolean {
  if (!value.startsWith("/")) {
    return false;
  }
  const queryIndex = value.indexOf("?");
  const pathPart = queryIndex >= 0 ? value.slice(0, queryIndex) : value;
  const segments = pathPart.split("/");
  return !segments.includes("..");
}

function validateByType(type: AnnotationType, value: string): boolean {
  switch (type) {
    case "token":
      return validateToken(value);
    case "size":
      return validateSize(value);
    case "path":
      return validatePath(value);
    default:
      return false;
  }
}

function validateValue(type: AnnotationType, value: string): Diagnostic[] {
  const diagnostics: Diagnostic[] = [];
  if (value.length === 0) {
    return [{ level: "error", code: "EMPTY_VALUE", message: "注解值不能为空", value }];
  }

  diagnostics.push(...forbiddenDiagnostics(value));
  if (startsWithHash(value)) {
    diagnostics.push({
      level: "error",
      code: "FORBIDDEN_HASH_PREFIX",
      message: "值不允许以 # 开头",
      value,
    });
  }

  if (diagnostics.length === 0 && !validateByType(type, value)) {
    diagnostics.push({
      level: "error",
      code: `INVALID_${type.toUpperCase()}`,
      message: `值不满足 ${type} 类型的格式要求`,
      value,
    });
  }
  return diagnostics;
}

/**
 * 括号与分号状态机复核。
 * 跟踪 {} 块深度（不允许为负，结束时必须回到 0），并确认每条指令都以分号结束：
 * 在遇到 `;`、`{` 或 `}` 时，待决语句必须已经包含非空白内容。
 */
export function verifyConfigStructure(config: string): StateMachineResult {
  const diagnostics: Diagnostic[] = [];
  let depth = 0;
  let pending = "";
  let inSingle = false;
  let inDouble = false;

  const flushCheck = (at: string): void => {
    if (pending.trim().length === 0) {
      diagnostics.push({
        level: "error",
        code: "EMPTY_STATEMENT",
        message: `遇到 ${at} 前缺少有效的指令内容`,
      });
    }
    pending = "";
  };

  for (let i = 0; i < config.length; i++) {
    const ch = config[i];

    if (inSingle) {
      if (ch === "'") inSingle = false;
      continue;
    }
    if (inDouble) {
      if (ch === '"') inDouble = false;
      continue;
    }

    if (ch === "'") {
      inSingle = true;
      pending += ch;
      continue;
    }
    if (ch === '"') {
      inDouble = true;
      pending += ch;
      continue;
    }
    if (ch === "#") {
      // 行注释：跳过到行尾，注释内容不参与语句判定。
      while (i < config.length && config[i] !== "\n") i++;
      continue;
    }

    if (ch === "{") {
      flushCheck("{");
      depth++;
      continue;
    }
    if (ch === "}") {
      if (pending.trim().length > 0) {
        diagnostics.push({
          level: "error",
          code: "UNTERMINATED_DIRECTIVE",
          message: "右花括号前的指令缺少分号结束",
        });
      }
      pending = "";
      depth--;
      if (depth < 0) {
        diagnostics.push({
          level: "error",
          code: "UNBALANCED_CLOSE_BRACE",
          message: "右花括号超出了已打开的块",
        });
      }
      continue;
    }
    if (ch === ";") {
      flushCheck(";");
      continue;
    }
    pending += ch;
  }

  if (inSingle || inDouble) {
    diagnostics.push({ level: "error", code: "UNTERMINATED_STRING", message: "字符串引号未闭合" });
  }
  if (pending.trim().length > 0) {
    diagnostics.push({ level: "error", code: "UNTERMINATED_DIRECTIVE", message: "结尾的指令缺少分号" });
  }
  if (depth !== 0) {
    diagnostics.push({
      level: "error",
      code: "UNCLOSED_BLOCK",
      message: `块深度未回到 0，当前深度为 ${depth}`,
    });
  }

  return { ok: diagnostics.length === 0, depth, diagnostics };
}

/** 复核一批指令名是否全部位于白名单内。 */
export function verifyDirectiveWhitelist(
  directives: readonly string[],
  allowed: readonly string[],
): Diagnostic[] {
  const allowedSet = new Set(allowed);
  const seen = new Set<string>();
  const diagnostics: Diagnostic[] = [];
  for (const name of directives) {
    if (seen.has(name)) continue;
    seen.add(name);
    if (!allowedSet.has(name)) {
      diagnostics.push({
        level: "error",
        code: "DIRECTIVE_NOT_ALLOWED",
        message: `指令 ${name} 不在调用方白名单内`,
        directive: name,
      });
    }
  }
  return diagnostics;
}

function safeIdentifier(template: DirectiveTemplate, key: string, diagnostics: Diagnostic[]): boolean {
  if (!NAME_RE.test(template.directive)) {
    diagnostics.push({
      level: "error",
      code: "INVALID_DIRECTIVE_NAME",
      message: `模板指令名 ${template.directive} 含非法字符`,
      key,
      directive: template.directive,
    });
    return false;
  }
  if (template.block !== undefined) {
    const head = template.block.trim();
    const headToken = head.split(/\s+/)[0];
    if (head.length === 0 || !NAME_RE.test(headToken)) {
      diagnostics.push({
        level: "error",
        code: "INVALID_BLOCK_HEADER",
        message: `块头 ${template.block} 不合法`,
        key,
      });
      return false;
    }
  }
  return true;
}

/**
 * 渲染注解为安全的配置片段，并对整份配置做结构与白名单复核。
 */
export function renderAnnotations(
  annotations: Annotations,
  options: RenderOptions,
): RenderResult {
  const diagnostics: Diagnostic[] = [];
  const { templates, allowedDirectives, baseConfig = "" } = options;

  const topLevel: string[] = [];
  const blocks = new Map<string, string[]>();
  const usedDirectives: string[] = [];

  for (const key of Object.keys(annotations)) {
    const template = templates[key];
    if (!template) {
      diagnostics.push({
        level: "warning",
        code: "UNKNOWN_ANNOTATION",
        message: `未知注解键 ${key} 已丢弃`,
        key,
      });
      continue;
    }

    if (!safeIdentifier(template, key, diagnostics)) {
      continue;
    }

    const value = annotations[key];
    const valueErrors = validateValue(template.type, value);
    if (valueErrors.length > 0) {
      for (const valueError of valueErrors) {
        diagnostics.push({ ...valueError, key, directive: template.directive });
      }
      continue;
    }

    const line = `${template.directive} ${value};`;
    if (template.block === undefined) {
      topLevel.push(line);
    } else {
      const header = template.block.trim();
      const bucket = blocks.get(header);
      if (bucket) {
        bucket.push(line);
      } else {
        blocks.set(header, [line]);
      }
    }
    if (!usedDirectives.includes(template.directive)) {
      usedDirectives.push(template.directive);
    }
  }

  const fragmentParts: string[] = [];
  if (topLevel.length > 0) {
    fragmentParts.push(topLevel.join("\n"));
  }
  for (const [header, lines] of blocks) {
    fragmentParts.push(`${header} {\n${lines.map((line) => `    ${line}`).join("\n")}\n}`);
  }
  const fragment = fragmentParts.join("\n");

  const whitelistDiagnostics = verifyDirectiveWhitelist(usedDirectives, allowedDirectives);
  diagnostics.push(...whitelistDiagnostics);

  const fullConfig = baseConfig.length > 0 ? `${baseConfig.replace(/\s+$/, "")}\n${fragment}` : fragment;
  const structure = verifyConfigStructure(fullConfig);
  diagnostics.push(...structure.diagnostics);

  const hasError = diagnostics.some((diagnostic) => diagnostic.level === "error");
  return {
    fragment,
    config: hasError ? null : fullConfig,
    ok: !hasError,
    diagnostics,
  };
}
