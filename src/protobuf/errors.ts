export class WireError extends Error {
  readonly offset: number;

  constructor(message: string, offset: number) {
    super(`[offset ${offset}] ${message}`);
    this.name = 'WireError';
    this.offset = offset;
  }
}

export class DecodeError extends Error {
  readonly path: string;

  constructor(path: string, message: string) {
    super(`${path}: ${message}`);
    this.name = 'DecodeError';
    this.path = path;
  }
}
