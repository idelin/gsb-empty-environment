/** AST builders and the sample programs used by the verification suite. */

import { Expr, PrimOp, Program } from "./lang";

export const num = (value: number): Expr => ({ tag: "num", value });
export const bool = (value: boolean): Expr => ({ tag: "bool", value });
export const v = (name: string): Expr => ({ tag: "var", name });
export const prim = (op: PrimOp, ...args: Expr[]): Expr => ({ tag: "prim", op, args });
export const iff = (cond: Expr, then: Expr, els: Expr): Expr =>
  ({ tag: "if", cond, then, else: els });
export const let_ = (name: string, value: Expr, body: Expr): Expr =>
  ({ tag: "let", name, value, body });
export const begin = (...exprs: Expr[]): Expr => ({ tag: "begin", exprs });
export const call = (name: string, ...args: Expr[]): Expr => ({ tag: "call", name, args });
export const raise = (message: string): Expr => ({ tag: "raise", message });

/** let x = 3 in (x + 4) * 2  ==>  14 */
export const arithProgram: Program = {
  functions: [],
  main: let_("x", num(3), prim("*", prim("+", v("x"), num(4)), num(2))),
};

/**
 * Non-tail recursion: fact(n) = if n <= 1 then 1 else n * fact(n - 1).
 * main adds 0 to the call so the top-level call is NOT in tail position.
 */
export function factorialProgram(n: number): Program {
  return {
    functions: [
      {
        name: "fact",
        params: ["n"],
        body: iff(
          prim("<=", v("n"), num(1)),
          num(1),
          prim("*", v("n"), call("fact", prim("-", v("n"), num(1))))
        ),
      },
    ],
    main: prim("+", call("fact", num(n)), num(0)),
  };
}

/** Pure tail recursion: countdown(n) = if n = 0 then 0 else countdown(n - 1). */
export function countdownProgram(n: number): Program {
  return {
    functions: [
      {
        name: "countdown",
        params: ["n"],
        body: iff(
          prim("=", v("n"), num(0)),
          num(0),
          call("countdown", prim("-", v("n"), num(1)))
        ),
      },
    ],
    main: call("countdown", num(n)),
  };
}

/** Mutual tail recursion: even/odd. */
export function evenOddProgram(n: number): Program {
  return {
    functions: [
      {
        name: "even",
        params: ["n"],
        body: iff(prim("=", v("n"), num(0)), bool(true), call("odd", prim("-", v("n"), num(1)))),
      },
      {
        name: "odd",
        params: ["n"],
        body: iff(prim("=", v("n"), num(0)), bool(false), call("even", prim("-", v("n"), num(1)))),
      },
    ],
    main: call("even", num(n)),
  };
}

/** Tail recursion with accumulator: sumTo(n, acc). */
export function sumToProgram(n: number): Program {
  return {
    functions: [
      {
        name: "sumTo",
        params: ["n", "acc"],
        body: iff(
          prim("=", v("n"), num(0)),
          v("acc"),
          call("sumTo", prim("-", v("n"), num(1)), prim("+", v("acc"), v("n")))
        ),
      },
    ],
    main: call("sumTo", num(n), num(0)),
  };
}

/** Call-order exercise: b(x) = a(x) + a(x); a(x) = x * 2. main = b(3) = 12. */
export const callOrderProgram: Program = {
  functions: [
    { name: "a", params: ["x"], body: prim("*", v("x"), num(2)) },
    { name: "b", params: ["x"], body: prim("+", call("a", v("x")), call("a", v("x"))) },
  ],
  main: call("b", num(3)),
};

/** Tree recursion: fib. Exercises non-tail call order heavily. */
export function fibProgram(n: number): Program {
  return {
    functions: [
      {
        name: "fib",
        params: ["n"],
        body: iff(
          prim("<", v("n"), num(2)),
          v("n"),
          prim("+",
            call("fib", prim("-", v("n"), num(1))),
            call("fib", prim("-", v("n"), num(2))))
        ),
      },
    ],
    main: call("fib", num(n)),
  };
}

/** Sequencing with calls in statement position (non-tail). */
export const beginProgram: Program = {
  functions: [
    { name: "g", params: ["x"], body: prim("+", v("x"), num(100)) },
    { name: "f", params: ["x"], body: prim("+", v("x"), num(10)) },
  ],
  main: begin(call("f", num(1)), call("g", num(2)), call("f", num(3))),
};

/** Error: undefined variable directly in main. */
export const undefVarProgram: Program = {
  functions: [],
  main: prim("+", v("nope"), num(1)),
};

/**
 * Error after a tail chain: chain(n) recurses tail-wise to 0 then raises.
 * The initial call sits under a `+`, so it is a NON-tail frame; the
 * self-recursion is tail. Report = 2 non-tail frames + bounded tail summary.
 */
export function tailChainErrorProgram(n: number): Program {
  return {
    functions: [
      {
        name: "chain",
        params: ["n"],
        body: iff(
          prim("=", v("n"), num(0)),
          raise("boom at the bottom"),
          call("chain", prim("-", v("n"), num(1)))
        ),
      },
    ],
    main: prim("+", call("chain", num(n)), num(1)),
  };
}

/**
 * Mixed non-tail + tail: outer() = 10 + inner(k); inner tail-recurses then
 * raises. main adds 0 so `outer` is a genuine non-tail frame; the non-tail
 * stack must show <main> -> outer -> inner.
 */
export function mixedErrorProgram(k: number): Program {
  return {
    functions: [
      {
        name: "outer",
        params: [],
        body: prim("+", num(10), call("inner", num(k))),
      },
      {
        name: "inner",
        params: ["n"],
        body: iff(
          prim("=", v("n"), num(0)),
          raise("inner blew up"),
          call("inner", prim("-", v("n"), num(1)))
        ),
      },
    ],
    main: prim("+", num(0), call("outer")),
  };
}
