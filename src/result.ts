// Shared result type for all checking engines.

export type CheckResult =
  | { status: 'equal' }
  | { status: 'not-equal'; counterexample: Record<string, bigint> }
  | { status: 'unknown'; reason: string };

export function formatResult(r: CheckResult): string {
  switch (r.status) {
    case 'equal':
      return 'equal';
    case 'unknown':
      return `unknown (${r.reason})`;
    case 'not-equal': {
      const parts = Object.entries(r.counterexample)
        .map(([k, v]) => `${k}=${v}`)
        .join(', ');
      return `not-equal, smallest counterexample: ${parts}`;
    }
  }
}
