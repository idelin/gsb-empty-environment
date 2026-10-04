#!/usr/bin/env -S node --experimental-strip-types
import { readFileSync } from 'node:fs';
import process from 'node:process';
import type { IssueConfig, TallyResult, Vote } from './types.ts';
import { EligibilityTable } from './eligibility.ts';
import { VoteLedger } from './ledger.ts';
import { recomputeTally, sameTally } from './recompute.ts';

interface CliInput {
  readonly issue: {
    readonly issueId: string;
    readonly eligibilityVersion: number;
    readonly candidates: readonly string[];
    readonly participationThreshold: string | number;
  };
  readonly eligibility: ReadonlyArray<{
    readonly version: number;
    readonly entries: ReadonlyArray<{
      readonly voterId: string;
      readonly weight: string | number;
    }>;
  }>;
  readonly votes: readonly Vote[];
}

function parseInput(raw: string): {
  issue: IssueConfig;
  eligibility: EligibilityTable;
  votes: Vote[];
} {
  const data = JSON.parse(raw) as CliInput;
  const issue: IssueConfig = {
    issueId: data.issue.issueId,
    eligibilityVersion: data.issue.eligibilityVersion,
    candidates: data.issue.candidates,
    participationThreshold: BigInt(data.issue.participationThreshold),
  };
  const eligibility = new EligibilityTable();
  for (const versioned of data.eligibility ?? []) {
    eligibility.publishVersion(
      versioned.version,
      versioned.entries.map(
        (entry) => [entry.voterId, BigInt(entry.weight)] as const,
      ),
    );
  }
  return { issue, eligibility, votes: [...(data.votes ?? [])] };
}

function formatTally(result: TallyResult): unknown {
  const candidateWeights: Record<string, string> = {};
  for (const [candidate, weight] of Object.entries(result.candidateWeights)) {
    candidateWeights[candidate] = weight.toString();
  }
  return {
    issueId: result.issueId,
    eligibilityVersion: result.eligibilityVersion,
    candidateWeights,
    participatedWeight: result.participatedWeight.toString(),
    participationThreshold: result.participationThreshold.toString(),
    resolved: result.resolved,
    winner: result.winner,
    reason: result.reason,
    rejected: result.rejected.map((entry) => ({
      reason: entry.reason,
      vote: entry.vote,
    })),
  };
}

function readInput(path: string): string {
  return path === '-' ? readFileSync(0, 'utf8') : readFileSync(path, 'utf8');
}

function usage(): never {
  process.stderr.write(
    'usage:\n' +
      '  vote-ledger tally <input.json|->   tally via the mergeable ledger\n' +
      '  vote-ledger verify <input.json|->  cross-check ledger vs recomputer\n',
  );
  process.exit(2);
}

function main(argv: string[]): void {
  const [command, path] = argv;
  if ((command !== 'tally' && command !== 'verify') || path === undefined) {
    usage();
  }
  const { issue, eligibility, votes } = parseInput(readInput(path));

  const ledger = new VoteLedger();
  ledger.addVotes(votes);
  const ledgerTally = ledger.tally(issue, eligibility);

  if (command === 'verify') {
    const recomputed = recomputeTally(votes, issue, eligibility);
    if (!sameTally(ledgerTally, recomputed)) {
      process.stderr.write('ledger and recomputer disagree\n');
      process.stdout.write(
        JSON.stringify(
          { ledger: formatTally(ledgerTally), recomputed: formatTally(recomputed) },
          null,
          2,
        ) + '\n',
      );
      process.exit(1);
    }
  }

  process.stdout.write(JSON.stringify(formatTally(ledgerTally), null, 2) + '\n');
}

try {
  main(process.argv.slice(2));
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  process.stderr.write(`error: ${message}\n`);
  process.exit(1);
}
