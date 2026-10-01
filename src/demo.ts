import { paragraphToNodes } from './nodes.ts';
import { breakParagraph } from './breaker.ts';
import { greedyBreak } from './greedy.ts';
import { exhaustiveBreak } from './exhaustive.ts';
import { createSession, applyEdit } from './incremental.ts';
import { formatGreedy, formatResult } from './report.ts';
import {
  BASE_OPTIONS,
  GREEDY_TRAP_OPTIONS,
  HYPHENATION,
  PARAGRAPHS,
} from './fixtures.ts';

function hr(label: string): void {
  console.log('\n' + '#'.repeat(72));
  console.log(`# ${label}`);
  console.log('#'.repeat(72));
}

// 1. Ordinary paragraph: hyphenation budget + no-consecutive rule active.
hr('1. optimal break with hyphenation constraints');
{
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const r = breakParagraph(nodes, BASE_OPTIONS);
  console.log(formatResult('simple paragraph', r));
  const narrowOptions = { ...BASE_OPTIONS, lineWidth: 100 };
  const smallNodes = paragraphToNodes(PARAGRAPHS.small, HYPHENATION);
  const smallDp = breakParagraph(smallNodes, narrowOptions);
  const ex = exhaustiveBreak(smallNodes, narrowOptions);
  console.log(`exhaustive check on small paragraph: DP=${smallDp.optimalCost} ` +
    `exhaustive=${ex.optimalCost} over ${ex.layouts} legal layouts ` +
    `=> ${ex.optimalCost === smallDp.optimalCost ? 'MATCHES' : 'MISMATCHES'}`);
}

// 2. Greedy trap: local first-fit is NOT optimal.
hr('2. greedy trap: local heuristic vs global optimum');
{
  const nodes = paragraphToNodes(PARAGRAPHS.greedyTrap, {});
  console.log(formatGreedy('greedy first-fit', greedyBreak(nodes, GREEDY_TRAP_OPTIONS)));
  const r = breakParagraph(nodes, GREEDY_TRAP_OPTIONS);
  console.log(formatResult('DP optimum', r));
}

// 3. Unbreakable word wider than the line: overflow, reported, no loop.
hr('3. unbreakable long word overflows (reported, terminates)');
{
  const nodes = paragraphToNodes(PARAGRAPHS.longWord, HYPHENATION);
  const narrow = { ...BASE_OPTIONS, lineWidth: 60 };
  console.log(formatResult('long word, lineWidth=60', breakParagraph(nodes, narrow)));
}

// 4. Hyphen-heavy paragraph under a tight hyphen budget.
hr('4. hyphen budget + consecutive-hyphen ban');
{
  const options = { ...BASE_OPTIONS, maxHyphens: 1 };
  const nodes = paragraphToNodes(PARAGRAPHS.hyphenHeavy, HYPHENATION);
  const r = breakParagraph(nodes, options);
  console.log(formatResult('hyphen-heavy, maxHyphens=1', r));
  const narrow = { ...options, lineWidth: 100 };
  const shortNodes = paragraphToNodes('international optimization beautiful', HYPHENATION);
  const shortDp = breakParagraph(shortNodes, narrow);
  const ex = exhaustiveBreak(shortNodes, narrow);
  console.log(`exhaustive check on short hyphen paragraph: DP=${shortDp.optimalCost} ` +
    `exhaustive=${ex.optimalCost} => ${ex.optimalCost === shortDp.optimalCost ? 'MATCHES' : 'MISMATCHES'}`);
}

// 5. Incremental edit: reuse the unaffected DP frontier.
hr('5. incremental edit with frontier reuse');
{
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const session = createSession(nodes, BASE_OPTIONS);
  console.log(`before edit: optimal cost ${session.result.optimalCost}, ` +
    `${session.result.stats.states} states`);
  const lazyIndex = nodes.findIndex((n) => n.kind === 'box' && n.text === 'lazy');
  const replacement = paragraphToNodes('energetic', HYPHENATION);
  const edit = applyEdit(session, lazyIndex, lazyIndex + 1, replacement);
  console.log(
    `edit: node ${lazyIndex} ("lazy") -> "energetic" | reused frontier up to position ` +
      `${edit.reuse.reusedUpToPosition} | reused states=${edit.reuse.reusedStates} ` +
      `recomputed states=${edit.reuse.recomputedStates} relaxations=${edit.reuse.relaxations}`,
  );
  console.log(formatResult('after edit', edit.result));
  const fresh = breakParagraph(session.nodes, BASE_OPTIONS);
  console.log(`full recompute cost=${fresh.optimalCost} => ` +
    `${fresh.optimalCost === edit.result.optimalCost ? 'incremental MATCHES full recompute' : 'MISMATCH'}`);
}

// 6. Budget interruption reported separately.
hr('6. budget exhaustion (reported, no optimality claim)');
{
  const nodes = paragraphToNodes(PARAGRAPHS.simple, HYPHENATION);
  const r = breakParagraph(nodes, { ...BASE_OPTIONS, budget: 5 });
  console.log(formatResult('tiny budget', r));
}
