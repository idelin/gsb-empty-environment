import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  IncrementalParser,
  fullParse,
  walkTree,
  type Diagnostic,
  type SyntaxNode,
} from '../src/index.ts';

const BASE_LINES = [
  '# 服务器配置',
  'server:',
  '    host = localhost',
  '    port = 8080',
  '    options = (',
  '        alpha,',
  '        beta,',
  '    )',
  '',
  'db:',
  '    url = db',
  '    pool = 4',
];
const BASE = BASE_LINES.join('\n');

function shape(node: SyntaxNode): unknown {
  return {
    kind: node.kind,
    indent: node.indent,
    text: node.text,
    diags: node.diagnostics.map((d) => d.code),
    children: node.children.map(shape),
  };
}

function idShape(node: SyntaxNode): unknown {
  return { id: node.id, children: node.children.map(idShape) };
}

function snapshotById(root: SyntaxNode): Map<string, SyntaxNode> {
  const map = new Map<string, SyntaxNode>();
  walkTree(root, (node) => map.set(node.id, node));
  return map;
}

function spliceLines(lines: string[], start: number, count: number, inserted: string[] = []): string[] {
  const copy = [...lines];
  copy.splice(start, count, ...inserted);
  return copy;
}

/** 增量结果必须与全新全量解析一致（结构 + 节点 id + 诊断）。 */
function assertSameAsFullParse(parser: IncrementalParser, text: string): void {
  const full = fullParse(text);
  assert.deepStrictEqual(shape(parser.root), shape(full.root), 'tree shape differs from full parse');
  assert.deepStrictEqual(idShape(parser.root), idShape(full.root), 'node ids differ from full parse');
  const pick = (ds: readonly Diagnostic[]) => ds.map((d) => [d.code, d.line, d.column]);
  assert.deepStrictEqual(pick(parser.diagnostics), pick(full.diagnostics), 'diagnostics differ from full parse');
}

/** 新树中所有 id 已存在的节点，必须与旧树是同一个对象（哈希一致化复用）。 */
function assertReusedByIdentity(before: Map<string, SyntaxNode>, root: SyntaxNode): { reused: number } {
  let reused = 0;
  walkTree(root, (node) => {
    const old = before.get(node.id);
    if (old) {
      assert.strictEqual(node, old, `node ${node.id} (${node.kind} ${node.text}) was not reused`);
      reused++;
    }
  });
  return { reused };
}

function findByText(root: SyntaxNode, text: string): SyntaxNode | undefined {
  let found: SyntaxNode | undefined;
  walkTree(root, (node) => {
    if (!found && node.text === text) found = node;
  });
  return found;
}

test('全量解析：块、注释、空行、括号续行', () => {
  const { root, diagnostics } = fullParse(BASE);
  assert.deepStrictEqual(diagnostics, []);
  assert.equal(root.kind, 'document');
  assert.deepStrictEqual(
    root.children.map((n) => n.kind),
    ['comment', 'line', 'line'],
  );
  const server = root.children[1];
  assert.equal(server.text, 'server:');
  assert.deepStrictEqual(
    server.children.map((n) => n.text),
    ['host = localhost', 'port = 8080', 'options = ('],
  );
  const options = server.children[2];
  assert.deepStrictEqual(
    options.children.map((n) => [n.kind, n.text]),
    [
      ['continuation', 'alpha,'],
      ['continuation', 'beta,'],
      ['continuation', ')'],
      ['blank', ''],
    ],
  );
  const db = root.children[2];
  assert.deepStrictEqual(
    db.children.map((n) => n.text),
    ['url = db', 'pool = 4'],
  );
});

test('Tab 缩进直接报错，但后续内容不丢失', () => {
  const parser = new IncrementalParser(['a:', '    x = 1', 'b:', '    z = 1'].join('\n'));
  const before = snapshotById(parser.root);
  const bSubtree = findByText(parser.root, 'b:');
  assert.ok(bSubtree);

  const result = parser.applyEdit({ startLine: 2, oldLineCount: 0, newLines: ['\tbad = 1', '    y = 1'] });
  const tabDiag = result.diagnostics.find((d) => d.code === 'tab-in-indent');
  assert.ok(tabDiag, 'expected tab-in-indent diagnostic');
  assert.equal(tabDiag.line, 2);

  // 错误行之后的 b 子树完整保留，且对象/ID 均未变化。
  const bAfter = findByText(result.root, 'b:');
  assert.ok(bAfter);
  assert.strictEqual(bAfter, bSubtree);
  assert.deepStrictEqual(shape(bAfter), {
    kind: 'line',
    indent: 0,
    text: 'b:',
    diags: [],
    children: [{ kind: 'line', indent: 4, text: 'z = 1', diags: [], children: [] }],
  });
  assert.ok(assertReusedByIdentity(before, result.root).reused >= 3);
  assertSameAsFullParse(parser, ['a:', '    x = 1', '\tbad = 1', '    y = 1', 'b:', '    z = 1'].join('\n'));
});

test('缩进回退错误局部化，后续块不受影响', () => {
  const clean = ['a:', '    x = 1', 'b:', '    z = 3'].join('\n');
  const cleanB = findByText(fullParse(clean).root, 'b:');
  assert.ok(cleanB);

  const parser = new IncrementalParser(clean);
  const result = parser.applyEdit({ startLine: 2, oldLineCount: 0, newLines: ['  y = 2'] });
  const dedent = result.diagnostics.find((d) => d.code === 'inconsistent-dedent');
  assert.ok(dedent, 'expected inconsistent-dedent diagnostic');
  assert.equal(dedent.line, 2);

  // 错误行恢复为 a 的子级；b 块与无错误的全量解析完全一致（同一 id）。
  const bAfter = findByText(result.root, 'b:');
  assert.ok(bAfter);
  assert.equal(bAfter.id, cleanB.id);
  assert.deepStrictEqual(
    result.root.children.map((n) => n.text),
    ['a:', 'b:'],
  );
  assertSameAsFullParse(parser, ['a:', '    x = 1', '  y = 2', 'b:', '    z = 3'].join('\n'));
});

test('未闭合/不匹配的括号产生诊断且不吞掉后续结构', () => {
  const { diagnostics } = fullParse(['x = 1)', 'y = (', 'z = 2'].join('\n'));
  assert.deepStrictEqual(
    diagnostics.map((d) => [d.code, d.line]),
    [
      ['unmatched-close', 0],
      ['unclosed-bracket', 1],
    ],
  );
});

test('增量：插入空行 -> 小影响范围，未变子树对象级复用', () => {
  const parser = new IncrementalParser(BASE);
  const before = snapshotById(parser.root);
  const dbBefore = findByText(parser.root, 'db:');
  const hostBefore = findByText(parser.root, 'host = localhost');
  assert.ok(dbBefore && hostBefore);

  const result = parser.applyEdit({ startLine: 9, oldLineCount: 0, newLines: ['', ''] });

  // 影响范围只覆盖插入点，未波及其他行。
  assert.deepStrictEqual(result.affectedRange, { startLine: 9, endLine: 11 });
  // 只有包含新空行的祖先链（root/server/options）是新建节点。
  assert.equal(result.stats.createdNodes, 3);
  assert.ok(result.stats.reusedNodes >= 10);
  // 未受影响的子树保持对象身份。
  assert.strictEqual(findByText(result.root, 'db:'), dbBefore);
  assert.strictEqual(findByText(result.root, 'host = localhost'), hostBefore);
  assertReusedByIdentity(before, result.root);
  // 与全量解析对照。
  assertSameAsFullParse(parser, spliceLines(BASE_LINES, 9, 0, ['', '']).join('\n'));
});

test('增量：删除闭括号 -> 续行重判 + 诊断，前文节点复用', () => {
  const parser = new IncrementalParser(BASE);
  const before = snapshotById(parser.root);
  const hostBefore = findByText(parser.root, 'host = localhost');
  const portBefore = findByText(parser.root, 'port = 8080');
  assert.ok(hostBefore && portBefore);

  // 删除第 7 行 "    )"，options 的括号从此未闭合。
  const result = parser.applyEdit({ startLine: 7, oldLineCount: 1, newLines: [] });

  const unclosed = result.diagnostics.find((d) => d.code === 'unclosed-bracket');
  assert.ok(unclosed, 'expected unclosed-bracket diagnostic');
  assert.equal(unclosed.line, 4);
  // 后续行被重判为续行，影响范围延伸到文件尾；
  // 起点是第 4 行：开括号所在行新增了 unclosed-bracket 诊断，也算受影响。
  assert.deepStrictEqual(result.affectedRange, { startLine: 4, endLine: 11 });
  // 编辑点之前的节点保持对象身份。
  assert.strictEqual(findByText(result.root, 'host = localhost'), hostBefore);
  assert.strictEqual(findByText(result.root, 'port = 8080'), portBefore);
  assertReusedByIdentity(before, result.root);
  assert.ok(result.stats.reusedNodes >= 6);
  // 与全量解析对照（db 块按语言规则降级为续行，而不是报错消失）。
  const after = spliceLines(BASE_LINES, 7, 1).join('\n');
  assertSameAsFullParse(parser, after);
  const dbLine = findByText(parser.root, 'db:');
  assert.equal(dbLine?.kind, 'continuation');
});

test('增量：跨块粘贴 -> 移动节点保持身份，与全量解析一致', () => {
  const parser = new IncrementalParser(BASE);
  const before = snapshotById(parser.root);
  const urlBefore = findByText(parser.root, 'url = db');
  const poolBefore = findByText(parser.root, 'pool = 4');
  const optionsBefore = findByText(parser.root, 'options = (');
  assert.ok(urlBefore && poolBefore && optionsBefore);

  // 1) 从 db 块剪出两行。
  const cut = parser.applyEdit({ startLine: 10, oldLineCount: 2, newLines: [] });
  const afterCut = spliceLines(BASE_LINES, 10, 2).join('\n');
  assertSameAsFullParse(parser, afterCut);
  assert.deepStrictEqual(cut.affectedRange, { startLine: 10, endLine: 11 });

  // 2) 粘贴进 server 块（options 之前）。
  const paste = parser.applyEdit({ startLine: 4, oldLineCount: 0, newLines: ['    url = db', '    pool = 4'] });
  const afterPaste = spliceLines(spliceLines(BASE_LINES, 10, 2), 4, 0, ['    url = db', '    pool = 4']).join('\n');
  assertSameAsFullParse(parser, afterPaste);

  // 被移动的两行仍是原来的对象（内容寻址复用），未触碰的 options 子树也不变。
  assert.strictEqual(findByText(paste.root, 'url = db'), urlBefore);
  assert.strictEqual(findByText(paste.root, 'pool = 4'), poolBefore);
  assert.strictEqual(findByText(paste.root, 'options = ('), optionsBefore);
  assertReusedByIdentity(before, paste.root);

  // 粘贴位置正确：server 块内、options 之前。
  const server = findByText(paste.root, 'server:');
  assert.ok(server);
  assert.deepStrictEqual(
    server.children.map((n) => n.text),
    ['host = localhost', 'port = 8080', 'url = db', 'pool = 4', 'options = ('],
  );
});

test('节点 ID 稳定：插入空行前后所有非空行节点 id 不变', () => {
  const parser = new IncrementalParser(BASE);
  const idsBefore = new Set<string>();
  walkTree(parser.root, (node) => {
    if (node.kind !== 'blank' && node.kind !== 'document') idsBefore.add(node.id);
  });
  const result = parser.applyEdit({ startLine: 0, oldLineCount: 0, newLines: [''] });
  const idsAfter = new Set<string>();
  walkTree(result.root, (node) => {
    if (node.kind !== 'blank' && node.kind !== 'document') idsAfter.add(node.id);
  });
  assert.deepStrictEqual(idsAfter, idsBefore);
});
