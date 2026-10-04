import {
  PartyDeclaration,
  Transcript,
  TranscriptVerification,
} from './types';
import {
  canonicalJson,
  hashDeclaration,
  sha256Hex,
} from './canonical';

function configHash(config: string[]): string {
  return sha256Hex(
    canonicalJson(config.slice().sort()),
  );
}

/** Compute the round identifier bound to both declarations. */
export function computeRoundId(
  declA: PartyDeclaration,
  declB: PartyDeclaration,
): string {
  const hashes: { [party: string]: string } = {};
  hashes[declA.party] = hashDeclaration(declA);
  hashes[declB.party] = hashDeclaration(declB);
  const rounds: { [party: string]: string } = {};
  rounds[declA.party] = declA.round;
  rounds[declB.party] = declB.round;
  return sha256Hex(
    'capneg-round-v1:' +
      canonicalJson({
        parties: [declA.party, declB.party].sort(),
        rounds,
        declarationHashes: hashes,
      }),
  );
}

/** Build a transcript binding the negotiation outcome to both declarations. */
export function buildTranscript(
  declA: PartyDeclaration,
  declB: PartyDeclaration,
  status: 'ok' | 'conflict',
  config: string[] | null,
): Transcript {
  const parties = [declA.party, declB.party].sort() as [string, string];
  const declarationHashes: { [party: string]: string } = {};
  declarationHashes[declA.party] = hashDeclaration(declA);
  declarationHashes[declB.party] = hashDeclaration(declB);
  const sortedConfig = config === null ? null : config.slice().sort();
  const body = {
    version: 1 as const,
    roundId: computeRoundId(declA, declB),
    parties,
    declarationHashes,
    status,
    config: sortedConfig,
    configHash: sortedConfig === null ? null : configHash(sortedConfig),
  };
  return {
    ...body,
    digest: sha256Hex('capneg-transcript-v1:' + canonicalJson(body)),
  };
}

/**
 * Verify a transcript against presented declarations.
 *
 * Each presented declaration is hashed and compared with the hash bound
 * in the transcript. A declaration from an older negotiation round (a
 * replay) hashes differently, so it is reported under `replayed` and the
 * verification fails — the transcript provably belongs to another round.
 */
export function verifyTranscript(
  transcript: Transcript,
  declarations: PartyDeclaration[],
): TranscriptVerification {
  const errors: string[] = [];
  const replayed: string[] = [];

  if (transcript.version !== 1) {
    errors.push(`unsupported transcript version: ${transcript.version}`);
  }

  const presented = new Map<string, PartyDeclaration>();
  for (const decl of declarations) {
    presented.set(decl.party, decl);
  }

  for (const party of transcript.parties) {
    const bound = transcript.declarationHashes[party];
    if (typeof bound !== 'string') {
      errors.push(`transcript binds no declaration hash for "${party}"`);
      continue;
    }
    const decl = presented.get(party);
    if (!decl) {
      errors.push(`no declaration presented for party "${party}"`);
      continue;
    }
    if (hashDeclaration(decl) !== bound) {
      replayed.push(party);
    }
  }

  for (const party of Object.keys(transcript.declarationHashes)) {
    if (transcript.parties.indexOf(party) < 0) {
      errors.push(`declaration hash bound for unknown party "${party}"`);
    }
  }

  // Recompute roundId from the bound declarations' rounds is impossible
  // without the originals, but the digest must be self-consistent.
  const body = {
    version: transcript.version,
    roundId: transcript.roundId,
    parties: transcript.parties,
    declarationHashes: transcript.declarationHashes,
    status: transcript.status,
    config: transcript.config,
    configHash: transcript.configHash,
  };
  const digest = sha256Hex('capneg-transcript-v1:' + canonicalJson(body));
  if (digest !== transcript.digest) {
    errors.push('transcript digest mismatch (tampered or malformed)');
  }

  if (transcript.status === 'ok') {
    if (transcript.config === null || transcript.configHash === null) {
      errors.push('ok transcript must carry config and configHash');
    } else if (configHash(transcript.config) !== transcript.configHash) {
      errors.push('config hash mismatch');
    }
  } else if (transcript.config !== null || transcript.configHash !== null) {
    errors.push('conflict transcript must not carry a config');
  }

  return { valid: errors.length === 0 && replayed.length === 0, replayed, errors };
}

/** True when the transcript binds exactly this declaration (same round). */
export function transcriptBinds(
  transcript: Transcript,
  decl: PartyDeclaration,
): boolean {
  return transcript.declarationHashes[decl.party] === hashDeclaration(decl);
}
