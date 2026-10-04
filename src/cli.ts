#!/usr/bin/env node
import { readFile } from 'node:fs/promises';
import { EligibilityTable } from './eligibility.ts';
import { VoteLedger } from './ledger.ts';
import { recount, tallyResultsEqual } from './recount.ts';
import { tallyIssue } from './tally.ts';
import type { Issue, TallyResult, VoteRecord } from './types.ts';

const USAGE = `Usage:
  node src/cli.ts tally   --issue <issue.json> --eligibility <eligibility.json> --votes <votes.json>...
  node src/cli.ts recount --issue <issue.json> --eligibility <eligibility.json> --votes <votes.json>...

Commands:
  tally    Merge votes into the ledger and print the tally as JSON.
  recount  Additionally recompute from the raw vote set with the independent
           recounter; exits non-zero if the two results disagree.
`;

interface CliArgs {
  command: string;
  issue?: string;
  eligibility?: string;
  votes: string[];
}

function parseArgs(argv: string[]): CliArgs {
  const [command, ...rest] = argv;
  if (command !== 'tally' && command !== 'recount') {
    throw new Error(`unknown command: ${command ?? '(none)'}`);
  }
  const args: CliArgs = { command, votes: [] };
  for (let i = 0; i < rest.length; i += 1) {
    const flag = rest[i];
    const value = rest[i + 1];
    if (value === undefined) throw new Error(`missing value for ${flag}`);
    switch (flag) {
      case '--issue':
        args.issue = value;
        i += 1;
        break;
      case '--eligibility':
        args.eligibility = value;
        i += 1;
        break;
      case '--votes':
        args.votes.push(value);
        i += 1;
        break;
      default:
        throw new Error(`unknown flag: ${flag}`);
    }
  }
  if (args.issue === undefined || args.eligibility === undefined || args.votes.length === 0) {
    throw new Error('--issue, --eligibility and at least one --votes are required');
  }
  return args;
}

async function loadJson(path: string): Promise<unknown> {
  return JSON.parse(await readFile(path, 'utf8'));
}

function toVote(raw: unknown): VoteRecord {
  const record = raw as Record<string, unknown>;
  return {
    issueId: record.issueId as string,
    voterId: record.voterId as string,
    version: record.version as number,
    replicaId: record.replicaId as string,
    choice: record.choice as VoteRecord['choice'],
  };
}

function toIssue(raw: unknown): Issue {
  const record = raw as Record<string, unknown>;
  return {
    issueId: record.issueId as string,
    eligibilityVersion: record.eligibilityVersion as number,
    participationThreshold: BigInt((record.participationThreshold as string | number | undefined) ?? 0),
  };
}

function toEligibility(raw: unknown): EligibilityTable {
  const table = new EligibilityTable();
  const versions = (raw as { versions: Array<{ version: number; weights: Record<string, string> }> }).versions;
  for (const { version, weights } of versions) {
    const parsed: Record<string, bigint> = {};
    for (const [voterId, weight] of Object.entries(weights)) {
      parsed[voterId] = BigInt(weight);
    }
    table.addVersion(version, parsed);
  }
  return table;
}

function resultToJson(result: TallyResult): unknown {
  return {
    issueId: result.issueId,
    eligibilityVersion: result.eligibilityVersion,
    decided: result.decided,
    winner: result.winner,
    participatedWeight: result.participatedWeight.toString(),
    candidateWeights: Object.fromEntries(
      [...result.candidateWeights.entries()].map(([candidateId, weight]) => [candidateId, weight.toString()]),
    ),
    rejected: result.rejected.map(({ vote, reason }) => ({
      issueId: vote.issueId,
      voterId: vote.voterId,
      version: vote.version,
      replicaId: vote.replicaId,
      reason,
    })),
  };
}

async function main(): Promise<void> {
  const args = parseArgs(process.argv.slice(2));
  const issue = toIssue(await loadJson(args.issue as string));
  const eligibility = toEligibility(await loadJson(args.eligibility as string));
  const rawVotes: VoteRecord[] = [];
  for (const path of args.votes) {
    const records = (await loadJson(path)) as unknown[];
    for (const record of records) rawVotes.push(toVote(record));
  }

  const ledger = new VoteLedger();
  ledger.import(rawVotes);
  const ledgerResult = tallyIssue(ledger, issue, eligibility);

  if (args.command === 'tally') {
    process.stdout.write(`${JSON.stringify(resultToJson(ledgerResult), null, 2)}\n`);
    return;
  }

  const recountResult = recount(rawVotes, issue, eligibility);
  const consistent = tallyResultsEqual(ledgerResult, recountResult);
  process.stdout.write(
    `${JSON.stringify(
      {
        consistent,
        ledger: resultToJson(ledgerResult),
        recount: resultToJson(recountResult),
      },
      null,
      2,
    )}\n`,
  );
  if (!consistent) {
    process.exitCode = 1;
  }
}

main().catch((error: unknown) => {
  process.stderr.write(`${error instanceof Error ? error.message : String(error)}\n\n${USAGE}`);
  process.exitCode = 1;
});
