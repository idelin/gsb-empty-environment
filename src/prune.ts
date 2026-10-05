/**
 * Conditional source pruning driven by `// #if` / `// #elif` / `// #else` /
 * `// #endif` directives. Directive lines and lines inside inactive branches
 * are removed; the result carries a bidirectional mapping back to the
 * original line numbers.
 */

import { scanLines } from './lexer.ts';
import { evaluate, parseExpression, type VarTable } from './expr.ts';
import { DirectiveError, ExpressionError } from './errors.ts';
import { LineMapping } from './mapping.ts';

export interface PruneOptions {
  /** Variables available to condition expressions. Unknown variables error. */
  vars?: VarTable;
}

export interface PruneResult {
  /** The pruned source text. */
  text: string;
  /** Bidirectional mapping between original and generated positions. */
  mapping: LineMapping;
}

interface Frame {
  /** Line of the `#if` that opened this frame. */
  ifLine: number;
  /** Whether the enclosing region is active. */
  parentActive: boolean;
  /** Whether any branch of this chain has already been taken. */
  branchTaken: boolean;
  /** Whether lines are currently being kept. */
  active: boolean;
  elseSeen: boolean;
}

function evalCondition(argument: string, line: number, vars: VarTable): boolean {
  if (argument === '') {
    throw new DirectiveError('missing condition expression', line);
  }
  try {
    return evaluate(parseExpression(argument), vars);
  } catch (err) {
    if (err instanceof ExpressionError) {
      throw new DirectiveError(`invalid condition: ${err.message}`, line);
    }
    throw err;
  }
}

export function pruneSource(source: string, options: PruneOptions = {}): PruneResult {
  const vars: VarTable = options.vars ?? {};
  const scanned = scanLines(source);
  const stack: Frame[] = [];
  const keptTexts: string[] = [];
  const keptLines: number[] = [];

  const currentActive = (): boolean =>
    stack.length === 0 || stack[stack.length - 1].active;

  const topFrame = (line: number, keyword: string): Frame => {
    const frame = stack[stack.length - 1];
    if (frame === undefined) {
      throw new DirectiveError(`#${keyword} without matching #if`, line);
    }
    return frame;
  };

  for (const sl of scanned) {
    const directive = sl.directive;
    if (directive === null) {
      if (currentActive()) {
        keptTexts.push(sl.text);
        keptLines.push(sl.line);
      }
      continue;
    }

    switch (directive.kind) {
      case 'if': {
        const parentActive = currentActive();
        // Conditions in inactive regions are not evaluated at all, so unknown
        // variables there cannot error (same laziness as short-circuiting).
        const condition = parentActive ? evalCondition(directive.argument, sl.line, vars) : false;
        stack.push({
          ifLine: sl.line,
          parentActive,
          branchTaken: condition,
          active: parentActive && condition,
          elseSeen: false,
        });
        break;
      }
      case 'elif': {
        const frame = topFrame(sl.line, 'elif');
        if (frame.elseSeen) {
          throw new DirectiveError('#elif after #else', sl.line);
        }
        if (!frame.parentActive || frame.branchTaken) {
          frame.active = false;
        } else {
          const condition = evalCondition(directive.argument, sl.line, vars);
          frame.branchTaken = condition;
          frame.active = condition;
        }
        break;
      }
      case 'else': {
        const frame = topFrame(sl.line, 'else');
        if (directive.argument !== '') {
          throw new DirectiveError('#else does not take a condition', sl.line);
        }
        if (frame.elseSeen) {
          throw new DirectiveError('duplicate #else', sl.line);
        }
        frame.elseSeen = true;
        frame.active = frame.parentActive && !frame.branchTaken;
        frame.branchTaken = true;
        break;
      }
      case 'endif': {
        if (directive.argument !== '') {
          throw new DirectiveError('#endif does not take a condition', sl.line);
        }
        if (stack.length === 0) {
          throw new DirectiveError('#endif without matching #if', sl.line);
        }
        stack.pop();
        break;
      }
    }
  }

  if (stack.length > 0) {
    const unclosed = stack[0];
    throw new DirectiveError(
      `unclosed conditional: #if at line ${unclosed.ifLine} has no matching #endif`,
      unclosed.ifLine,
    );
  }

  let text = keptTexts.join('\n');
  if (source.endsWith('\n') && keptTexts.length > 0) text += '\n';

  return { text, mapping: new LineMapping(keptLines, scanned.length) };
}
