import type { Document, GlyphMetrics } from './types.ts';

/** Fixture glyph metrics: uniform 10-unit glyphs, 5-unit space. */
export const DEMO_METRICS: GlyphMetrics = {
  charWidths: {},
  defaultCharWidth: 10,
  spaceWidth: 5,
  lineHeight: 10,
  headingHeight: 16,
  maxLineWidth: 150,
};

export function demoDocument(): Document {
  return {
    id: 'demo',
    blocks: [
      { kind: 'heading', id: 'h1', text: 'Overview' },
      {
        kind: 'paragraph',
        id: 'p1',
        text: 'alpha beta gamma[^fn1] delta epsilon zeta eta theta iota kappa lambda mu nu xi',
      },
      { kind: 'block', id: 'fig1', height: 45 },
      {
        kind: 'paragraph',
        id: 'p2',
        text: 'omicron pi rho[^fn2] sigma tau upsilon phi chi psi omega aleph beth gimel',
      },
    ],
    footnotes: {
      fn1: { text: 'first note' },
      fn2: { text: 'second note' },
    },
  };
}

/** Small enough for the exhaustive verifier to enumerate every pagination. */
export function smallDocument(): Document {
  return {
    id: 'small',
    blocks: [
      { kind: 'heading', id: 'h1', text: 'Tiny' },
      { kind: 'paragraph', id: 'p1', text: 'aaaa bbbb[^fn1] cccc dddd' },
      { kind: 'block', id: 'fig1', height: 10 },
    ],
    footnotes: {
      fn1: { text: 'note' },
    },
  };
}
