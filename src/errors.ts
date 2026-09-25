import { formatPath, type Path } from './path.ts';

/** Wire-format level corruption (bad tags, truncated varints, ...). */
export class FormatError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'FormatError';
  }
}

/** Tree-build / validation failure. `at` always locates the offending path. */
export class BuildError extends Error {
  readonly at: string;
  constructor(message: string, path: Path) {
    const at = formatPath(path);
    super(`${message} (at ${at})`);
    this.name = 'BuildError';
    this.at = at;
  }
}
