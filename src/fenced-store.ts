export class StaleFencingTokenError extends Error {
  readonly highestToken: number;
  readonly receivedToken: number;

  constructor(highestToken: number, receivedToken: number) {
    super(
      `stale fencing token: received ${receivedToken}, but a write with token ${highestToken} was already accepted`,
    );
    this.name = "StaleFencingTokenError";
    this.highestToken = highestToken;
    this.receivedToken = receivedToken;
  }
}

export class FencedStore {
  private highestToken = 0;
  private readonly data = new Map<string, string>();

  write(fencingToken: number, key: string, value: string): void {
    if (fencingToken < this.highestToken) {
      throw new StaleFencingTokenError(this.highestToken, fencingToken);
    }
    this.highestToken = fencingToken;
    this.data.set(key, value);
  }

  read(key: string): string | undefined {
    return this.data.get(key);
  }
}
