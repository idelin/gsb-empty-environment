import { strict as assert } from "assert";
import { compile, exec, RegexSyntaxError } from "../src/regex";
import { refExec, RefResult } from "./reference";

type Norm = { start: number; end: number; groups: ([number, number] | null)[] } | null;

function normLib(r: ReturnType<typeof exec>): Norm {
  if (r === null) return null;
  return {
    start: r.start,
    end: r.end,
    groups: r.groups.map((g) => (g === null ? null : ([g.start, g.end] as [number, number]))),
  };
}

function normRef(r: RefResult | null): Norm {
  return r === null ? null : { start: r.start, end: r.end, groups: r.groups };
}

let checks = 0;

function expect(pattern: string, text: string, expected: Norm): void {
  const actual = normLib(exec(pattern, text));
  assert.deepStrictEqual(
    actual,
    expected,
    `exec(${JSON.stringify(pattern)}, ${JSON.stringify(text)})`,
  );
  checks++;
}

// ---------------------------------------------------------------------------
// 1. 显式用例（人工推导的期望，不依赖任何正则引擎）
// ---------------------------------------------------------------------------

// 基本：字面量 / 左most / 无匹配
expect("b", "ab", { start: 1, end: 2, groups: [] });
expect("(b)", "aab", { start: 2, end: 3, groups: [[2, 3]] });
expect("(a)b", "aaa", null);
expect("", "ab", { start: 0, end: 0, groups: [] });
expect("a*", "baa", { start: 0, end: 0, groups: [] }); // 最左优先于更长

// 未参与组（null）与空组（[i,i]）必须区分
expect("()", "ab", { start: 0, end: 0, groups: [[0, 0]] });
expect("(a)|(b)", "b", { start: 0, end: 1, groups: [null, [0, 1]] });
expect("(a*)*", "b", { start: 0, end: 0, groups: [[0, 0]] }); // 空迭代使组参与
expect("()*", "a", { start: 0, end: 0, groups: [[0, 0]] });
expect("(|)", "a", { start: 0, end: 0, groups: [[0, 0]] });
expect("(a)|(a*)", "a", { start: 0, end: 1, groups: [[0, 1], null] }); // 组序优先：g1 参与胜出

// 最左最长整体匹配
expect("(a|ab)(c|bcd)", "abcd", { start: 0, end: 4, groups: [[0, 1], [1, 4]] });
expect("(a*)(a*)", "aa", { start: 0, end: 2, groups: [[0, 2], [2, 2]] });
expect("(a*)(b*)(c*)", "aab", { start: 0, end: 3, groups: [[0, 2], [2, 3], [3, 3]] });
expect("(a|b)*abb", "ababb", { start: 0, end: 5, groups: [[1, 2]] });
expect("(a*)b", "aab", { start: 0, end: 3, groups: [[0, 2]] });
expect("(a*)*b", "aab", { start: 0, end: 3, groups: [[0, 2]] });

// 嵌套星号 / 空分支
expect("(a*)*", "aa", { start: 0, end: 2, groups: [[0, 2]] });
expect("((a*)*)*", "aa", { start: 0, end: 2, groups: [[0, 2], [0, 2]] });
expect("(|a)*", "a", { start: 0, end: 1, groups: [[0, 1]] });
expect("(|a)*", "b", { start: 0, end: 0, groups: [[0, 0]] });
expect("(a|)*", "b", { start: 0, end: 0, groups: [[0, 0]] });
expect("((|a)b*)*", "ab", { start: 0, end: 2, groups: [[0, 2], [0, 1]] });
expect("(a|aa)*", "aa", { start: 0, end: 2, groups: [[0, 2]] });
expect("(aa|a)*a", "aa", { start: 0, end: 2, groups: [[0, 1]] });

// 星号迭代重置体内组：最后一轮未参与 => null
expect("((a)|b)*", "ab", { start: 0, end: 2, groups: [[1, 2], null] });
expect("((a)|(b))*", "ab", { start: 0, end: 2, groups: [[1, 2], null, [1, 2]] });

// 星号内组取最后一轮捕获
expect("(a(b|c)*d)", "abcbd", { start: 0, end: 5, groups: [[0, 5], [3, 4]] });

// 转义
expect("a\\*b", "a*b", { start: 0, end: 3, groups: [] });
expect("\\(a\\)", "(a)", { start: 0, end: 3, groups: [] });

// 语法错误
assert.throws(() => compile("("), RegexSyntaxError);
assert.throws(() => compile(")"), RegexSyntaxError);
assert.throws(() => compile("*"), RegexSyntaxError);
assert.throws(() => compile("a\\"), RegexSyntaxError);
checks += 4;

// ---------------------------------------------------------------------------
// 2. 穷举交叉核对：小字母表全部短串 × 一组含嵌套空分支的模式
//    参考答案来自 test/reference.ts 的独立实现（有界展开 + 状态集模拟），
//    不使用宿主正则结果。
// ---------------------------------------------------------------------------

const PATTERNS = [
  "a", "b", "ab", "a|b", "", "()", "(a)", "(a|b)", "(a)(b)",
  "a*", "(a*)", "a**", "(a)*", "(a*)*", "((a*)*)*", "(a*)(a*)",
  "()*", "(()*)", "((())*)*", "(|)", "(|)*", "(|a)", "(|a)*", "(a|)*",
  "(a|b)*", "((a)|(b))*", "((a)|b)*", "(a|b)*abb", "(a|ab)(c|bcd)",
  "(a*)b", "(a*)*b", "((|a)b*)*", "(a|)*a", "(a*)(b*)", "(a)|(b)",
  "(a|aa)*", "(aa|a)*a", "(|a)(|b)", "(a|)(b|)", "((a*))*b", "(a)|(a*)",
  "(x)|(a*)", "x(a|)y", "(a(b|c)*d)", "(a*)(b*)(c*)", "(|a|b)*", "((a*)b*)*",
];

function* allStrings(alphabet: string[], maxLen: number): Generator<string> {
  yield "";
  if (maxLen === 0) return;
  for (const rest of allStrings(alphabet, maxLen - 1)) {
    for (const ch of alphabet) yield ch + rest;
  }
}

const EXTRA_STRINGS = ["xay", "abcd", "ababb", "dcba", "xy", "aacbb"];

let crossChecks = 0;
for (const pattern of PATTERNS) {
  const re = compile(pattern);
  const texts = [...allStrings(["a", "b"], 4), ...EXTRA_STRINGS];
  for (const text of texts) {
    const libResult = normLib(re.exec(text));
    const refResult = normRef(refExec(pattern, text));
    assert.deepStrictEqual(
      libResult,
      refResult,
      `mismatch: pattern=${JSON.stringify(pattern)} text=${JSON.stringify(text)}`,
    );
    crossChecks++;
  }
}

console.log(`ok - ${checks} targeted checks, ${crossChecks} exhaustive cross-checks`);
