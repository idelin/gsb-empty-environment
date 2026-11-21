import { TArray, THash } from "./collections.ts";
import { TVal, TaintRegistry, previewValue, type Literal, type Val } from "./tval.ts";

export interface CaptureGroup {
  /** 捕获序号，0 表示整个匹配 $&。 */
  index: number;
  value: TVal<string>;
}

export interface CaptureResult {
  matched: boolean;
  /** 整串匹配锚定且模式不含 .* 时为 true，捕获组据此清洗。 */
  cleansed: boolean;
  groups: CaptureGroup[];
  /** 等价于 groups[0]，即整个匹配。 */
  match: TVal<string> | undefined;
}

/**
 * 脚本检查宿主：为分析中的 Perl 风格脚本操作提供污点传播语义。
 * 每个宿主拥有独立的污点事实图，脚本中所有 TVal 都归属于该宿主。
 */
export class TaintHost {
  readonly registry: TaintRegistry;

  constructor(registry: TaintRegistry = new TaintRegistry()) {
    this.registry = registry;
  }

  /**
   * 标记外部输入为污点来源，等价于 Perl 中来自 %ENV、<>、参数等的变量。
   */
  source(name: string, raw: Literal): TVal {
    const id = this.registry.origin(name, raw);
    return new TVal(raw, [id], this.registry);
  }

  /** 把裸字面值提升为干净 TVal；TVal 原样返回。 */
  box(value: Val): TVal {
    if (value instanceof TVal) {
      return value;
    }
    return new TVal(value, [], this.registry);
  }

  /** 赋值 $a = $b：完整复制右值污点事实。 */
  assign(value: Val): TVal {
    const right = this.box(value);
    const id = this.registry.link("assign", `=${previewValue(right.raw)}`, [
      ...right.factIds,
    ]);
    return new TVal(
      right.raw,
      id === undefined ? [] : [id],
      this.registry,
    );
  }

  /** 字符串拼接 $a . $b：任一操作数污点则结果污点，父事实全部保留。 */
  concat(left: Val, right: Val): TVal<string> {
    const lhs = this.box(left);
    const rhs = this.box(right);
    const parents = [...lhs.factIds, ...rhs.factIds];
    const id = this.registry.link(
      "concat",
      `${previewValue(lhs.raw)} . ${previewValue(rhs.raw)}`,
      parents,
    );
    return new TVal(`${lhs.raw ?? ""}${rhs.raw ?? ""}`, wrap(id), this.registry);
  }

  /**
   * 模板插值，用法 host.interpolate`cmd=${arg}`。
   * 任意插值位为污点，整个字符串即污点。
   */
  interpolate(strings: TemplateStringsArray, ...values: Val[]): TVal<string> {
    let output = "";
    const parents: number[] = [];
    values.forEach((value, index) => {
      const tval = this.box(value);
      output += strings[index] ?? "";
      output += tval.raw ?? "";
      parents.push(...tval.factIds);
    });
    output += strings[strings.length - 1] ?? "";
    const id = this.registry.link("interpolate", previewValue(output), parents);
    return new TVal(output, wrap(id), this.registry);
  }

  /** join $sep, @arr：任一元素或分隔符污点，结果污点。 */
  join(separator: Val, array: TArray): TVal<string> {
    const sep = this.box(separator);
    const elements = array.toTVals();
    const parents = [...sep.factIds];
    const parts = elements.map((element) => {
      parents.push(...element.factIds);
      return String(element.raw ?? "");
    });
    const id = this.registry.link(
      "join",
      `sep=${previewValue(sep.raw)}`,
      parents,
    );
    return new TVal(parts.join(String(sep.raw ?? "")), wrap(id), this.registry);
  }

  /** substr($s, $off, $len?)：子串沿用原串污点（不做任何清洗）。 */
  substr(value: Val, offset: number, length?: number): TVal<string> {
    const source = this.box(value);
    const text = String(source.raw ?? "");
    const cut = length === undefined ? text.slice(offset) : text.slice(offset, offset + length);
    const id = this.registry.link(
      "substr",
      `[${offset}${length === undefined ? "" : `,${length}`}]=${previewValue(cut)}`,
      [...source.factIds],
    );
    return new TVal(cut, wrap(id), this.registry);
  }

  array(initial: Val[] = []): TArray {
    return new TArray(this.registry, initial);
  }

  hash(): THash {
    return new THash(this.registry);
  }

  /**
   * 正则捕获，模拟 ($x) = $input =~ /pattern/。
   *
   * 本题规则：仅当模式为整串锚定（^…$，且无 m 标志）并且模式源码不含
   * `.*` 时，捕获组才算清洗（白名单）；未锚定、含 .* 或带 m 标志的模式，
   * 其捕获组一律沿用输入污点——这正是攻击者“借未锚定模式洗掉污点”的防线。
   */
  capture(input: Val, pattern: RegExp): CaptureResult {
    const source = this.box(input);
    const text = String(source.raw ?? "");
    const match = new RegExp(pattern.source, pattern.flags).exec(text);

    if (!match) {
      return { matched: false, cleansed: false, groups: [], match: undefined };
    }

    const verdict = anchoringVerdict(pattern);
    const cleansed = verdict.allowed;
    const groups: CaptureGroup[] = [];

    match.forEach((captured, index) => {
      const raw = captured ?? "";
      let tval: TVal<string>;
      if (cleansed) {
        // 锚定白名单：捕获子串被清洗，事实链在此终止。
        tval = new TVal(raw, [], this.registry);
      } else {
        const id = this.registry.link(
          "capture",
          `/${pattern.source}/${index === 0 ? "match" : `g${index}`}${verdict.reason === "" ? "" : ` ${verdict.reason}`}`,
          [...source.factIds],
        );
        tval = new TVal(raw, wrap(id), this.registry);
      }
      groups.push({ index, value: tval });
    });

    return {
      matched: true,
      cleansed,
      groups,
      match: groups[0]?.value,
    };
  }
}

export interface AnchoringVerdict {
  allowed: boolean;
  reason: string;
}

/**
 * 判定模式是否满足“整串锚定且不含 .*”的清洗条件。
 * 锚定要求源码以 ^ 开头、以 $ 结尾；m 标志会让 ^/$ 退化为行边界，故拒绝。
 */
export function anchoringVerdict(pattern: RegExp): AnchoringVerdict {
  const source = pattern.source;
  if (pattern.flags.includes("m")) {
    return { allowed: false, reason: "anchor-defeated-by-m-flag" };
  }
  if (source.includes(".*")) {
    return { allowed: false, reason: "pattern-contains-.*" };
  }
  const anchored = source.startsWith("^") && source.endsWith("$");
  if (!anchored) {
    return { allowed: false, reason: "not-whole-string-anchored" };
  }
  return { allowed: true, reason: "" };
}

function wrap(id: number | undefined): number[] {
  return id === undefined ? [] : [id];
}
