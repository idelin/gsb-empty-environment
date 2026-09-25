// Flatten the input node tree into a concatenated source string plus a
// cluster stream annotated with source offsets, direction, link and key
// metadata. All later mapping (hit test, selection, accessible name)
// refers to offsets in this source string.

import type { BlockInput, ClusterItem, Dir, InlineNode } from './types.ts';
import { splitClusters } from './cluster.ts';
import { paragraphDir, resolveClusterDir, strongDir } from './bidi.ts';

interface Ctx {
  dir?: Dir;
  linkId?: string;
  href?: string;
  key: boolean;
  required: boolean;
  keyId?: string;
  atomicId: number;
}

function isCjk(cp: number): boolean {
  return (cp >= 0x4e00 && cp <= 0x9fff) || (cp >= 0x3040 && cp <= 0x30ff) ||
    (cp >= 0xac00 && cp <= 0xd7af) || (cp >= 0x3000 && cp <= 0x303f) ||
    (cp >= 0xff00 && cp <= 0xffef);
}

function breakAfterCluster(text: string): boolean {
  if (/^\s+$/.test(text)) return true;
  const cps = [...text];
  const cp = cps[cps.length - 1].codePointAt(0) as number;
  return isCjk(cp);
}

export function flattenBlock(block: BlockInput): { source: string; items: ClusterItem[]; paraDir: Dir } {
  // Pass 1: concatenate the full source text (accessible name).
  let source = '';
  const collect = (nodes: InlineNode[]): void => {
    for (const n of nodes) {
      if (n.kind === 'link') collect(n.children);
      else source += n.text;
    }
  };
  collect(block.children);
  const paraDir = paragraphDir(source, block.dir);

  // Pass 2: emit clusters with metadata.
  const items: ClusterItem[] = [];
  let offset = 0;
  let atomicCounter = 0;

  const emitText = (text: string, ctx: Ctx): void => {
    for (const span of splitClusters(text)) {
      const inherited = ctx.dir ?? paraDir;
      items.push({
        kind: 'cluster',
        text: span.text,
        srcStart: offset + span.start,
        srcEnd: offset + span.end,
        dir: resolveClusterDir(span.text, inherited),
        linkId: ctx.linkId,
        href: ctx.href,
        key: ctx.key,
        required: ctx.required,
        keyId: ctx.keyId,
        atomicId: ctx.atomicId,
        breakAfter: breakAfterCluster(span.text),
        width: 0,
      });
    }
    offset += text.length;
  };

  const walk = (nodes: InlineNode[], ctx: Ctx): void => {
    for (const n of nodes) {
      if (n.kind === 'text') {
        emitText(n.text, {
          ...ctx,
          dir: n.dir ?? ctx.dir,
          key: n.key ?? ctx.key,
          required: n.required ?? ctx.required,
          keyId: n.keyId ?? ctx.keyId,
        });
      } else if (n.kind === 'entity') {
        const atomicId = ++atomicCounter;
        emitText(n.text, {
          ...ctx,
          dir: n.dir ?? ctx.dir,
          key: n.key ?? ctx.key,
          required: n.required ?? ctx.required,
          keyId: n.keyId ?? ctx.keyId,
          atomicId,
        });
      } else {
        walk(n.children, { ...ctx, linkId: n.id, href: n.href });
      }
    }
  };

  walk(block.children, { key: false, required: false, atomicId: 0 });
  return { source, items, paraDir };
}
