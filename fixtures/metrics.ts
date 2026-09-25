// Fixture glyph metric tables. Widths are arbitrary device-independent
// units; v2/v3 simulate a late-arriving real font with different
// metrics, and wideEllipsis models a font whose ellipsis glyph is
// wider than a narrow column.

import type { MetricTable } from '../src/types.ts';

const cjk: Record<string, number> = {};
for (const ch of ['进', '度', '完', '成', '详', '见', '报', '告', '，', '已', '全', '部']) {
  cjk[ch] = 12;
}

export const metricsV1: MetricTable = {
  version: 1,
  defaultWidth: 6,
  spaceWidth: 3,
  ellipsisWidth: 10,
  clusters: {
    ...cjk,
    '…': 10,
    '👨‍👩‍👧‍👦': 16,
    '👍': 16,
    '🎉': 16,
    'é': 6, // e + combining acute, one cluster
  },
};

export const metricsV2: MetricTable = {
  version: 2,
  defaultWidth: 8,
  spaceWidth: 4,
  ellipsisWidth: 14,
  clusters: {
    ...Object.fromEntries(Object.keys(cjk).map((k) => [k, 16])),
    '…': 14,
    '👨‍👩‍👧‍👦': 20,
    '👍': 20,
    '🎉': 20,
    'é': 8,
  },
};

export const metricsV3: MetricTable = {
  version: 3,
  defaultWidth: 5,
  spaceWidth: 2,
  ellipsisWidth: 8,
  clusters: {
    ...Object.fromEntries(Object.keys(cjk).map((k) => [k, 10])),
    '…': 8,
    '👨‍👩‍👧‍👦': 14,
    '👍': 14,
    '🎉': 14,
    'é': 5,
  },
};

export const metricsWideEllipsis: MetricTable = {
  version: 1,
  defaultWidth: 6,
  spaceWidth: 3,
  ellipsisWidth: 50,
  clusters: { '…': 50 },
};
