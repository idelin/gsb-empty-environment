import { Rational } from './rational.ts';
import { ScrollNode } from './scroll-node.ts';
import type { Rect } from './scroll-node.ts';
import { ScrollWorld } from './world.ts';
import { vec } from './types.ts';

/**
 * 夹具字形度量：等宽为主、少量宽字符，行高固定。
 * 核心排版计算在这里完成，不借浏览器布局引擎。
 */
export const GLYPH_ADVANCES: Readonly<Record<string, string>> = {
  ' ': '3',
  i: '3',
  l: '3',
  '.': '3',
  ',': '3',
  w: '9',
  m: '9',
  '汉': '12',
  '字': '12',
  '宽': '12',
  '混': '12',
  '排': '12',
  '测': '12',
  '试': '12',
  '。': '12',
};
export const DEFAULT_ADVANCE = '6';
export const LINE_HEIGHT = '14';

export interface TextMetrics {
  readonly lineHeight: Rational;
  advance(ch: string): Rational;
}

export function makeMetrics(): TextMetrics {
  return {
    lineHeight: Rational.of(LINE_HEIGHT),
    advance(ch: string): Rational {
      return Rational.of(GLYPH_ADVANCES[ch] ?? DEFAULT_ADVANCE);
    },
  };
}

export interface TextLayout {
  readonly lines: number;
  readonly width: Rational;
  readonly height: Rational;
}

/** 贪心折行：逐字累加前进量，超宽换行；内容高 = 行数 × 行高。 */
export function layoutText(text: string, metrics: TextMetrics, maxWidth: Rational): TextLayout {
  let lines = 1;
  let lineWidth = Rational.zero();
  let maxLineWidth = Rational.zero();
  for (const ch of text) {
    if (ch === '\n') {
      maxLineWidth = Rational.max(maxLineWidth, lineWidth);
      lineWidth = Rational.zero();
      lines++;
      continue;
    }
    const advance = metrics.advance(ch);
    if (!lineWidth.isZero() && lineWidth.add(advance).cmp(maxWidth) > 0) {
      maxLineWidth = Rational.max(maxLineWidth, lineWidth);
      lineWidth = advance;
      lines++;
    } else {
      lineWidth = lineWidth.add(advance);
    }
  }
  maxLineWidth = Rational.max(maxLineWidth, lineWidth);
  return {
    lines,
    width: maxLineWidth,
    height: metrics.lineHeight.mul(Rational.of(BigInt(lines))),
  };
}

export const FIXTURE_TEXT = 'the quick brown fox jumps over the lazy dog. '.repeat(60) + '汉字宽字符混排测试。';

function rectOf(x: string, y: string, width: string, height: string): Rect {
  return { x: Rational.of(x), y: Rational.of(y), width: Rational.of(width), height: Rational.of(height) };
}

/**
 * 夹具节点树（根坐标矩形全部显式给出）：
 *   root(400x300, 仅纵滚, 边界锁定)
 *   └─ outer(360x260, 纵滚, 内容高由字形排版得出)
 *      ├─ inner(200x120, 纵滚)
 *      ├─ strip(200x40, 横滚, 轴变换 y→x)
 *      ├─ locked(120x120, 纵滚, 边界锁定)
 *      └─ both(120x100, 双轴)
 */
export function buildFixtureWorld(): ScrollWorld {
  const metrics = makeMetrics();
  const outerLayout = layoutText(FIXTURE_TEXT, metrics, Rational.of('340'));

  const root = new ScrollNode('root', {
    viewport: vec('400', '300'),
    content: vec('400', '900'),
    axes: ['y'],
    overscroll: { y: 'lock' },
    rect: rectOf('0', '0', '400', '300'),
  });
  const outer = new ScrollNode('outer', {
    viewport: vec('360', '260'),
    content: vec('360', outerLayout.height.toString()),
    axes: ['y'],
    overscroll: { y: 'chain' },
    rect: rectOf('20', '20', '360', '260'),
  });
  const inner = new ScrollNode('inner', {
    viewport: vec('200', '120'),
    content: vec('200', '480'),
    axes: ['y'],
    overscroll: { y: 'chain' },
    rect: rectOf('40', '40', '200', '120'),
  });
  const strip = new ScrollNode('strip', {
    viewport: vec('200', '40'),
    content: vec('640', '40'),
    axes: ['x'],
    overscroll: { x: 'chain' },
    rect: rectOf('40', '180', '200', '40'),
    axisTransform: (d) => vec(d.x.add(d.y), '0'),
  });
  const locked = new ScrollNode('locked', {
    viewport: vec('120', '120'),
    content: vec('120', '360'),
    axes: ['y'],
    overscroll: { y: 'lock' },
    rect: rectOf('260', '40', '120', '120'),
  });
  const both = new ScrollNode('both', {
    viewport: vec('120', '100'),
    content: vec('300', '400'),
    axes: ['x', 'y'],
    overscroll: { x: 'chain', y: 'chain' },
    rect: rectOf('260', '180', '120', '100'),
  });

  root.addChild(outer);
  outer.addChild(inner);
  outer.addChild(strip);
  outer.addChild(locked);
  outer.addChild(both);
  return new ScrollWorld(root);
}
