import type { HttpsResponse, HttpsFetcher, Logger, DnsResolver } from "../index.ts";
import type { MtaStsMode } from "../index.ts";

export class FakeClock {
  current: number;
  constructor(start = 1_000_000_000_000) {
    this.current = start;
  }
  now(): number {
    return this.current;
  }
  advance(ms: number): void {
    this.current += ms;
  }
}

export function policyText(mode: MtaStsMode, maxAge: number, mx: string[]): string {
  return [
    "version: STSv1",
    `mode: ${mode}`,
    `max_age: ${maxAge}`,
    ...mx.map((host) => `mx: ${host}`),
    "",
  ].join("\n");
}

export function txtRecord(id: string): string {
  return `v=STSv1; id=${id}`;
}

interface ResolverState {
  records: string[];
  calls: number;
  error?: Error;
}

export class FakeResolver implements DnsResolver {
  private states = new Map<string, ResolverState>();
  calls = 0;

  setRecords(domain: string, records: string[]): void {
    this.states.set(domain.toLowerCase(), { records, calls: 0 });
  }

  removeRecords(domain: string): void {
    this.setRecords(domain, []);
  }

  failWith(domain: string, error: Error): void {
    this.states.set(domain.toLowerCase(), { records: [], calls: 0, error });
  }

  lookupCount(domain: string): number {
    return this.states.get(domain.toLowerCase())?.calls ?? 0;
  }

  async resolveStsTxt(domain: string): Promise<string[]> {
    this.calls += 1;
    const key = domain.toLowerCase();
    const state = this.states.get(key);
    if (state === undefined) {
      return [];
    }
    state.calls += 1;
    if (state.error !== undefined) {
      throw state.error;
    }
    return [...state.records];
  }
}

export class FakeFetcher implements HttpsFetcher {
  calls: string[] = [];
  private responses = new Map<string, HttpsResponse>();
  private errors = new Map<string, Error>();

  setResponse(url: string, body: string, status = 200): void {
    this.responses.set(url, { url, status, body });
  }

  fail(url: string, error: Error): void {
    this.errors.set(url, error);
  }

  async fetch(url: string): Promise<HttpsResponse> {
    this.calls.push(url);
    const error = this.errors.get(url);
    if (error !== undefined) {
      throw error;
    }
    const response = this.responses.get(url);
    if (response === undefined) {
      throw new Error(`unexpected fetch: ${url}`);
    }
    return { ...response };
  }
}

export class RecordingLogger implements Logger {
  warnings: Array<{ message: string; details?: Record<string, unknown> }> = [];

  warn(message: string, details?: Record<string, unknown>): void {
    this.warnings.push({ message, details });
  }
}

export function policyUrl(domain: string): string {
  return `https://mta-sts.${domain}/.well-known/mta-sts.txt`;
}
