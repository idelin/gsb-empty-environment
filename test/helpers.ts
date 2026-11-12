import { mkdtempSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import type { TestContext } from 'node:test';

export function createTempDirectory(context: TestContext): string {
  const root = process.env.TMPDIR;
  if (!root) {
    throw new Error('TMPDIR must point at the task-specific temp directory');
  }
  const directory = mkdtempSync(join(root, 'legacy-header-policy-'));
  context.after(() => rmSync(directory, { recursive: true, force: true }));
  return directory;
}
