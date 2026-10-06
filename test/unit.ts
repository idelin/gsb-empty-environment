import assert from 'node:assert';
import { compile, extractFirst } from '../src/index';
import { parse, print } from '../src/index';

interface Expected {
  index: number;
  match: string;
  groups: (string | undefined)[];
}

function check(pattern: string, input: string, expected: Expected | null): void {
  const actual = compile(pattern).exec(input);
  assert.deepStrictEqual(
    actual,
    expected,
    `模式 /${pattern}/ 作用于 ${JSON.stringify(input)}：期望 ${JSON.stringify(expected)}，实际 ${JSON.stringify(actual)}`,
  );
}

export function runUnitTests(): number {
  let n = 0;
  const t = (pattern: string, input: string, expected: Expected | null) => {
    check(pattern, input, expected);
    n++;
  };

  // 字面字符与连接
  t('abc', 'xxabcx', { index: 2, match: 'abc', groups: [] });
  t('abc', 'ab', null);
  t('', 'x', { index: 0, match: '', groups: [] });

  // 或：最长整体匹配优先于分支顺序
  t('a|ab', 'ab', { index: 0, match: 'ab', groups: [] });
  t('ab|a', 'ab', { index: 0, match: 'ab', groups: [] });
  t('a|b', 'xb', { index: 1, match: 'b', groups: [] });

  // 最左起点
  t('b', 'aab', { index: 2, match: 'b', groups: [] });
  t('a*', 'b', { index: 0, match: '', groups: [] });
  t('a*', 'aabaa', { index: 0, match: 'aa', groups: [] });

  // 捕获组：整体最长优先，再按组序最长
  t('(a|ab)(c|bcd)', 'abcd', { index: 0, match: 'abcd', groups: ['a', 'bcd'] });
  t('(a)(b)', 'abc', { index: 0, match: 'ab', groups: ['a', 'b'] });
  t('a(b)c', 'abc', { index: 0, match: 'abc', groups: ['b'] });

  // 未参与组（undefined）与空串（''）不同
  t('(a)|(b)', 'b', { index: 0, match: 'b', groups: [undefined, 'b'] });
  t('(a)|(b)', 'a', { index: 0, match: 'a', groups: ['a', undefined] });
  t('()', 'x', { index: 0, match: '', groups: [''] });
  t('(a|)', 'b', { index: 0, match: '', groups: [''] });

  // 星号与空匹配循环：必须终止
  t('(a*)*', 'b', { index: 0, match: '', groups: [''] });
  t('(a*)*', 'aa', { index: 0, match: 'aa', groups: ['aa'] });
  t('(a|)*', 'a', { index: 0, match: 'a', groups: ['a'] });
  t('(a|)*', '', { index: 0, match: '', groups: [''] });
  t('((a*)*)*', 'aaa', { index: 0, match: 'aaa', groups: ['aaa', 'aaa'] });
  // 空前迭代可先于消费字符的迭代：(b*) 先空匹配置 g3=''，再由 (a*) 消费 'a'
  t('((a*)|(b*))*', 'a', { index: 0, match: 'a', groups: ['a', 'a', ''] });
  t('((a)|(b))*', 'ab', { index: 0, match: 'ab', groups: ['b', 'a', 'b'] });

  // 组序最长：组 1 优先于组 2
  t('(a|ab)(b|)', 'ab', { index: 0, match: 'ab', groups: ['ab', ''] });
  t('(a|)(ab|b)', 'ab', { index: 0, match: 'ab', groups: ['a', 'b'] });

  // 连续星号等价于嵌套星号
  t('a**', 'aaa', { index: 0, match: 'aaa', groups: [] });

  // 输入长度 20：穷举 + 记忆化必须快速终止
  const twenty = 'ab'.repeat(10);
  t('(a|b)*', twenty, { index: 0, match: twenty, groups: ['b'] });
  t('a*', 'a'.repeat(20), { index: 0, match: 'a'.repeat(20), groups: [] });
  t('((a*)*)*', 'a'.repeat(20), {
    index: 0,
    match: 'a'.repeat(20),
    groups: ['a'.repeat(20), 'a'.repeat(20)],
  });
  t('(ab)*(cd|)', 'ababcdab', { index: 0, match: 'ababcd', groups: ['ab', 'cd'] });

  // 日志提取场景
  const line = '2026-10-06 08:00:01 INFO user=alice action=login';
  const r = extractFirst('user=(a|b|c|d|e|f|g|h|i|j|k|l|m|n|o|p|q|r|s|t|u|v|w|x|y|z)* action=', line);
  assert.deepStrictEqual(r && r.groups[0], 'e', '日志提取应取到 user 的最后一个字符');
  n++;

  // 解析器：往返与报错
  for (const p of ['a', 'a|b', '(a)', '(a|b)*c', '((a*)*)*', '', '()', '(a|)']) {
    const { ast, groupCount } = parse(p);
    const printed = print(ast);
    const reparsed = parse(printed);
    assert.strictEqual(reparsed.groupCount, groupCount, `往返后组数应一致：/${p}/ -> /${printed}/`);
    assert.strictEqual(print(reparsed.ast), printed, `往返打印应稳定：/${p}/ -> /${printed}/`);
    n++;
  }
  for (const bad of ['(', ')', 'a)', '*a', '(a', 'a|*b']) {
    assert.throws(() => parse(bad), SyntaxError, `/${bad}/ 应当解析失败`);
    n++;
  }

  return n;
}
