/**
 * 差分测试：随机生成模式与输入，比较
 *   - 匹配器（src/matcher.ts，记忆化 + 不动点）
 *   - 预言机（test/oracle.ts，显式枚举解析树）
 * 两者实现路径完全独立，结果必须一致。
 */
import assert from 'node:assert';
import { Ast, countGroups, print } from '../src/ast';
import { parse } from '../src/parser';
import { compile } from '../src/index';
import { oracleExec } from './oracle';

/** 可复现的伪随机数发生器。 */
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a |= 0;
    a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

class Gen {
  private nextId = 1;
  groupCount = 0;
  constructor(private readonly rand: () => number) {}

  private id(): number {
    return this.nextId++;
  }

  private pick<T>(xs: T[]): T {
    return xs[Math.floor(this.rand() * xs.length)]!;
  }

  ast(depth: number): Ast {
    // 所有分支均有返回；最后的 throw 仅为满足控制流分析
    if (depth <= 0) {
      // 叶子：字面字符或 epsilon（空连接）
      return this.rand() < 0.7
        ? { kind: 'char', id: this.id(), ch: this.pick(['a', 'b']) }
        : { kind: 'concat', id: this.id(), parts: [] };
    }
    const kind = this.pick(['char', 'concat', 'alt', 'star', 'group', 'star', 'group']);
    switch (kind) {
      case 'char':
        return { kind: 'char', id: this.id(), ch: this.pick(['a', 'b']) };
      case 'concat': {
        const n = 1 + Math.floor(this.rand() * 3);
        const parts: Ast[] = [];
        for (let i = 0; i < n; i++) parts.push(this.ast(depth - 1));
        return { kind: 'concat', id: this.id(), parts };
      }
      case 'alt': {
        const n = 2 + Math.floor(this.rand() * 2);
        const options: Ast[] = [];
        for (let i = 0; i < n; i++) options.push(this.ast(depth - 1));
        return { kind: 'alt', id: this.id(), options };
      }
      case 'star':
        return { kind: 'star', id: this.id(), child: this.ast(depth - 1) };
      case 'group': {
        const index = ++this.groupCount;
        return { kind: 'group', id: this.id(), index, child: this.ast(depth - 1) };
      }
    }
    throw new Error('unreachable');
  }

  input(maxLen: number): string {
    const len = Math.floor(this.rand() * (maxLen + 1));
    let s = '';
    for (let i = 0; i < len; i++) s += this.pick(['a', 'b']);
    return s;
  }
}

export function runDifferentialTests(seed: number, cases: number): number {
  const rand = mulberry32(seed);
  for (let i = 0; i < cases; i++) {
    const gen = new Gen(rand);
    const depth = 1 + Math.floor(rand() * 3); // 1..3
    const ast = gen.ast(depth);
    assert.strictEqual(countGroups(ast), gen.groupCount, 'countGroups 应与生成器一致');

    // 生成的 AST 打印成模式串后必须能重新解析（检验解析器与打印器）
    const pattern = print(ast);
    const reparsed = parse(pattern);
    assert.strictEqual(
      print(reparsed.ast),
      pattern,
      `打印-解析往返失败：/${pattern}/ -> /${print(reparsed.ast)}/`,
    );

    const maxLen = rand() < 0.85 ? 8 : 14;
    const input = gen.input(maxLen);

    const viaMatcher = compile(pattern).exec(input);
    const viaOracle = oracleExec(reparsed.ast, reparsed.groupCount, input);
    assert.deepStrictEqual(
      viaMatcher,
      viaOracle,
      `第 ${i} 例不一致：模式 /${pattern}/ 输入 ${JSON.stringify(input)}\n` +
        `匹配器: ${JSON.stringify(viaMatcher)}\n预言机: ${JSON.stringify(viaOracle)}`,
    );
  }
  return cases;
}
