/**
 * 顶层差分判定。
 *
 * 裁决顺序：
 * 1. 执行两个解析回调（抛异常归一化为 internal 错误的拒绝）。
 * 2. 接受/拒绝结局不可归并：一边拒绝一边接受 => acceptance_mismatch。
 * 3. 双方都拒绝 => 比较错误类别（同类别只报告位置差异，不伪造语义分歧）。
 * 4. 双方都接受 => 先比较消费字节数，再做类型树差分。
 */

import { diffTrees } from "./diff.ts";
import type {
  ComparisonResult,
  Divergence,
  ParseError,
  ParseOutcome,
  ParserCallback,
  SideEvidence,
  TypeNode,
} from "./types.ts";

export type {
  ByteRange,
  ChildNode,
  ComparisonResult,
  Divergence,
  DivergenceCategory,
  ErrorCategory,
  ParseError,
  ParseOutcome,
  ParserCallback,
  PathSegment,
  Severity,
  SideEvidence,
  SideSummary,
  TypeNode,
} from "./types.ts";
export { diffTrees } from "./diff.ts";
export { fingerprint } from "./diff.ts";
export { semanticEqual, stableStringify } from "./value.ts";

function runParser(parser: ParserCallback, input: Uint8Array): ParseOutcome {
  try {
    return parser(input);
  } catch (cause) {
    const error: ParseError = {
      category: "internal",
      message: cause instanceof Error ? cause.message : String(cause),
      thrown: true,
    };
    return { accepted: false, error };
  }
}

function errorEvidence(error: ParseError, consumed?: number): SideEvidence {
  return { type: "error", error, consumed };
}

function makeTop(
  category: Divergence["category"],
  severity: Divergence["severity"],
  semantic: boolean,
  left: SideEvidence | null,
  right: SideEvidence | null,
  detail: string,
): Divergence {
  return { category, severity, semantic, path: [], left, right, detail };
}

/** 比较两个协议解析实现对同一份输入的解析结果。 */
export function compare(
  input: Uint8Array,
  leftParser: ParserCallback,
  rightParser: ParserCallback,
): ComparisonResult {
  const leftOutcome = runParser(leftParser, input);
  const rightOutcome = runParser(rightParser, input);

  const leftSummary = {
    accepted: leftOutcome.accepted,
    consumed: leftOutcome.consumed,
    error: leftOutcome.accepted ? undefined : leftOutcome.error,
  };
  const rightSummary = {
    accepted: rightOutcome.accepted,
    consumed: rightOutcome.consumed,
    error: rightOutcome.accepted ? undefined : rightOutcome.error,
  };

  const trees = {
    left: leftOutcome.accepted ? leftOutcome.tree : (leftOutcome.tree ?? null),
    right: rightOutcome.accepted ? rightOutcome.tree : (rightOutcome.tree ?? null),
  };

  const base: ComparisonResult = {
    inputLength: input.length,
    left: leftSummary,
    right: rightSummary,
    trees,
    divergences: [],
    equivalent: true,
  };

  if (leftOutcome.accepted !== rightOutcome.accepted) {
    // 接受 vs 拒绝：禁止归并，保留双方各自证据。
    const rejected = (leftOutcome.accepted ? rightOutcome : leftOutcome) as Extract<
      ParseOutcome,
      { accepted: false }
    >;
    base.divergences = [
      makeTop(
        "acceptance_mismatch",
        "semantic",
        true,
        leftOutcome.accepted
          ? null
          : errorEvidence(rejected.error, rejected.consumed),
        rightOutcome.accepted
          ? null
          : errorEvidence(rejected.error, rejected.consumed),
        `one side accepted while the other rejected (${rejected.error.category})`,
      ),
    ];
  } else if (!leftOutcome.accepted && !rightOutcome.accepted) {
    const leftError = leftOutcome.error;
    const rightError = rightOutcome.error;

    if (leftError.category !== rightError.category) {
      base.divergences = [
        makeTop(
          "reject_category_mismatch",
          "semantic",
          true,
          errorEvidence(leftError, leftOutcome.consumed),
          errorEvidence(rightError, rightOutcome.consumed),
          `rejection categories differ: ${leftError.category} vs ${rightError.category}`,
        ),
      ];
    } else {
      // 同类别拒绝：类别一致则语义上等价，位置差异仅作 informational 记录。
      const divergences: Divergence[] = [];
      if (!sameOptionalRange(leftError.range, rightError.range)) {
        divergences.push(
          makeTop(
            "error_location_mismatch",
            "informational",
            false,
            errorEvidence(leftError, leftOutcome.consumed),
            errorEvidence(rightError, rightOutcome.consumed),
            `both reject as ${leftError.category} but at different byte locations`,
          ),
        );
      }
      base.divergences = divergences;
    }
  } else {
    const leftAcceptedOutcome = leftOutcome as Extract<ParseOutcome, { accepted: true }>;
    const rightAcceptedOutcome = rightOutcome as Extract<ParseOutcome, { accepted: true }>;
    const divergences: Divergence[] = [];

    if (leftAcceptedOutcome.consumed !== rightAcceptedOutcome.consumed) {
      divergences.push(
        makeTop(
          "consumed_length_mismatch",
          "structural",
          true,
          { type: "input", consumed: leftAcceptedOutcome.consumed },
          { type: "input", consumed: rightAcceptedOutcome.consumed },
          `consumed byte counts differ: ${leftAcceptedOutcome.consumed} vs ${rightAcceptedOutcome.consumed}`,
        ),
      );
    }

    divergences.push(...diffTrees(leftAcceptedOutcome.tree, rightAcceptedOutcome.tree));
    base.divergences = divergences;
  }

  base.equivalent = !base.divergences.some((item) => item.semantic);
  return base;
}

function sameOptionalRange(
  left: TypeNode["range"] | undefined,
  right: TypeNode["range"] | undefined,
): boolean {
  if (left === undefined || right === undefined) return left === right;
  return left.start === right.start && left.end === right.end;
}
