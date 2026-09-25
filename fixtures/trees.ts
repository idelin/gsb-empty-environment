// Fixture node trees. The engine never touches a DOM; these plain
// objects are the whole input model.

import type { BlockInput } from '../src/types.ts';

export const englishParagraph: BlockInput = {
  children: [
    { kind: 'text', text: 'The quick brown fox jumps over the lazy dog and keeps running through the forest' },
  ],
};

export const mixedWithEmoji: BlockInput = {
  children: [
    { kind: 'text', text: '进度 ' },
    { kind: 'text', text: '👨‍👩‍👧‍👦' },
    { kind: 'text', text: ' 已完成，详见报告' },
  ],
};

export const fileName: BlockInput = {
  children: [
    { kind: 'text', text: 'annual-report-2024-final-revision-3.pdf' },
  ],
};

export const linkSentence: BlockInput = {
  children: [
    { kind: 'text', text: 'Open ' },
    {
      kind: 'link',
      id: 'L1',
      href: 'https://example.com/docs',
      children: [{ kind: 'text', text: 'the complete reference documentation' }],
    },
    { kind: 'text', text: ' now' },
  ],
};

export const hebrewBlock: BlockInput = {
  children: [
    { kind: 'text', text: 'אבגדה' },
  ],
};

export const hebrewMixed: BlockInput = {
  children: [
    { kind: 'text', text: 'אבג' },
    { kind: 'text', text: ' ' },
    { kind: 'text', text: 'abc' },
    { kind: 'text', text: ' ' },
    { kind: 'text', text: 'דהו' },
  ],
};

export const entitySentence: BlockInput = {
  children: [
    { kind: 'text', text: 'Thanks ' },
    { kind: 'entity', id: 'mention-1', text: '@AliceSmith' },
    { kind: 'text', text: ' for the detailed review of this code' },
  ],
};

export const keyLog: BlockInput = {
  children: [
    { kind: 'text', text: 'ERROR ' },
    { kind: 'text', text: 'db-primary-01', key: true, keyId: 'host' },
    { kind: 'text', text: ' failed: ' },
    { kind: 'text', text: 'E_CONN_REFUSED', key: true, required: true, keyId: 'code' },
  ],
};

export const keyChoice: BlockInput = {
  children: [
    { kind: 'text', text: 'STARTER', key: true, keyId: 'A' },
    { kind: 'text', text: ' middle padding here ' },
    { kind: 'text', text: 'ENDER', key: true, keyId: 'B' },
  ],
};

export const conflictingKeys: BlockInput = {
  children: [
    { kind: 'entity', id: 'k1', text: 'AAAAAAAAAA', key: true, required: true, keyId: 'k1' },
    { kind: 'text', text: ' ' },
    { kind: 'entity', id: 'k2', text: 'BBBBBBBBBB', key: true, required: true, keyId: 'k2' },
  ],
};

export const combiningMark: BlockInput = {
  children: [
    { kind: 'text', text: 'café! au lait' },
  ],
};
