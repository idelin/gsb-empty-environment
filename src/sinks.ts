import type { TaintHost } from "./host.ts";
import { TVal, type ChainStep, type OriginFact, type Val } from "./tval.ts";

export type SinkName = "system" | "exec" | "backtick" | "open-write";

/** 写模式 open 的模式字符（> 截断写、>> 追加写、| 管道写）。 */
const WRITE_MODES = new Set([">", ">>", "|-", "|"]);

export interface SinkResult {
  sink: SinkName;
  command: string;
  allowed: boolean;
}

export interface OpenResult {
  sink: "open-write";
  mode: string;
  path: string;
  allowed: boolean;
}

/**
 * 污点流入危险宿主时抛出。携带：
 * - sink：危险操作类型
 * - value：触发违规的污点值
 * - chain：主来源链（逐步操作 + 事实 id + 父事实 id）
 * - origins：可达的全部污点根来源
 */
export class TaintViolation extends Error {
  readonly chain: ChainStep[];
  readonly origins: OriginFact[];
  readonly sink: SinkName;
  readonly value: TVal;

  constructor(sink: SinkName, value: TVal) {
    super(
      `taint violation: tainted value reaches ${sink} ` +
        `(value=${String(value.raw)}, chain=${value.chainString()})`,
    );
    this.name = "TaintViolation";
    this.sink = sink;
    this.value = value;
    this.chain = value.chain();
    this.origins = value.roots();
  }
}

/**
 * 危险宿主管控：模拟 Perl 的 system、exec、反引号和 open。
 * 任何污点参数流入都会抛出 TaintViolation，违规对象中给出完整来源链。
 */
export class SinkHost {
  private readonly host: TaintHost;

  constructor(host: TaintHost) {
    this.host = host;
  }

  system(command: Val): SinkResult {
    const arg = this.guard("system", command);
    return { sink: "system", command: String(arg.raw), allowed: true };
  }

  exec(command: Val): SinkResult {
    const arg = this.guard("exec", command);
    return { sink: "exec", command: String(arg.raw), allowed: true };
  }

  /** 反引号捕获 `cmd`。 */
  backtick(command: Val): SinkResult {
    const arg = this.guard("backtick", command);
    return { sink: "backtick", command: String(arg.raw), allowed: true };
  }

  /**
   * 两参数 open($fh, $expr)：$expr 形如 "> /path"、">> /path"、"| cmd"。
   * 写模式（含管道写）下路径/命令为污点即违规；读模式 "<" 不在本题管控范围。
   */
  open(expression: Val): OpenResult {
    const arg = this.host.box(expression);
    const text = String(arg.raw ?? "").trimStart();
    const mode = detectMode(text);
    if (mode !== undefined && WRITE_MODES.has(mode) && arg.isTainted) {
      throw new TaintViolation("open-write", arg);
    }
    return {
      sink: "open-write",
      mode: mode ?? "<",
      path: mode === undefined ? text : text.slice(mode.length).trimStart(),
      allowed: true,
    };
  }

  private guard(sink: SinkName, command: Val): TVal {
    const arg = this.host.box(command);
    if (arg.isTainted) {
      throw new TaintViolation(sink, arg);
    }
    return arg;
  }
}

/** 从 open 表达式前缀解析模式字符。 */
export function detectMode(expression: string): string | undefined {
  if (expression.startsWith(">>")) {
    return ">>";
  }
  if (expression.startsWith(">")) {
    return ">";
  }
  if (expression.startsWith("|-")) {
    return "|-";
  }
  if (expression.startsWith("|")) {
    return "|";
  }
  if (expression.startsWith("<")) {
    return "<";
  }
  return undefined;
}
