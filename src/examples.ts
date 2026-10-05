// 示例程序。

import {
  add,
  assertThat,
  assign,
  bin,
  iff,
  inp,
  land,
  mul,
  num,
  program,
  resetIds,
  sub,
  v,
  whileDo,
} from "./ast.ts";
import type { Program } from "./ast.ts";

// 主示例：赋值、分支、有界循环、合并、溢出。
//   a = x + 60
//   if (a > 10) b = a * 2 else b = a - 100
//   b = b + y
//   i = 0
//   while (i < 3) { b = b + x; i = i + 1 }
//   assert(b != 0)
export function demoProgram(): Program {
  resetIds();
  return program(
    ["x", "y"],
    [
      assign("a", add(inp("x"), num(60))),
      iff(
        bin("gt", v("a"), num(10)),
        [assign("b", mul(v("a"), num(2)))],
        [assign("b", sub(v("a"), num(100)))],
      ),
      assign("b", add(v("b"), inp("y"))),
      assign("i", num(0)),
      whileDo(bin("lt", v("i"), num(3)), [
        assign("b", add(v("b"), inp("x"))),
        assign("i", add(v("i"), num(1))),
      ]),
      assertThat(bin("ne", v("b"), num(0)), "b != 0"),
    ],
    8,
  );
}

// 含不可达分支的程序：
// - x > 100 与 x < 0 不可能同时成立；
// - x 是 8 位有符号数，x > 127 恒假。
export function unreachableProgram(): Program {
  resetIds();
  return program(
    ["x"],
    [
      iff(bin("gt", inp("x"), num(100)), [assign("y", num(1))], [assign("y", num(2))]),
      iff(bin("eq", v("y"), num(1)), [
        iff(
          land(bin("lt", inp("x"), num(0)), bin("gt", inp("x"), num(100))),
          [assign("z", num(1))],
          [assign("z", num(2))],
        ),
        assertThat(bin("ne", v("z"), num(1)), "z != 1"),
      ]),
      iff(bin("gt", inp("x"), num(127)), [assign("w", num(1))]),
    ],
    4,
  );
}

// 断言恒成立的安全程序：a = x & 15 必在 [0, 15] 内。
export function safeProgram(): Program {
  resetIds();
  return program(
    ["x"],
    [
      assign("a", bin("and", inp("x"), num(15))),
      assertThat(land(bin("ge", v("a"), num(0)), bin("le", v("a"), num(15))), "0 <= a <= 15"),
    ],
    4,
  );
}
