import type { EventKind, TraceEvent } from './types.ts';

const LINE_PATTERN =
  /^(?<ts>\S+)\s+\[(?<server>[^\]]+)\]\s+rpc=(?<rpc>\S+)\s+(?<kind>REQ_SEND|REQ_RECV|RESP_SEND|RESP_RECV)\s*$/;

/**
 * Parses one log line per event:
 *   2026-10-04T12:00:00.000+08:00 [server-A] rpc=req-1 REQ_SEND
 *
 * Timestamps are ISO-8601 and may carry any timezone offset; the instant is
 * normalised to epoch ms, while clock *drift* (distinct from timezone) is
 * left for the causal corrector.
 */
export function parseLogLine(line: string, index: number): TraceEvent {
  const match = LINE_PATTERN.exec(line.trim());
  if (!match || !match.groups) {
    throw new Error('unparseable log line #' + (index + 1) + ': ' + line);
  }
  const { ts, server, rpc, kind } = match.groups;
  const time = Date.parse(ts);
  if (Number.isNaN(time)) {
    throw new Error('invalid timestamp on line #' + (index + 1) + ': ' + ts);
  }
  return {
    id: server + '#' + rpc + '#' + kind,
    server,
    rpcId: rpc,
    kind: kind as EventKind,
    time,
  };
}

export function parseLog(text: string): TraceEvent[] {
  return text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith('#'))
    .map((line, index) => parseLogLine(line, index));
}
