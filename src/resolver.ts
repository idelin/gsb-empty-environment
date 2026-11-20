import type {
  AnchorVerifier,
  Branch,
  ResolutionReason,
  ResolutionResult,
} from './types.js';

/** 分支结构或输入不合法时抛出。 */
export class ConflictResolutionError extends Error {}

interface EvaluatedBranch {
  branch: Branch;
  tipCid: string;
  length: number;
  /** 链上最后一个通过校验的锚定提交的下标；无有效锚定时为 undefined。 */
  anchorCommitIndex?: number;
  anchorTimestamp?: number;
}

/**
 * 校验单条分支的结构完整性：
 * - 提交链非空；
 * - 首提交为创世提交（cid === genesisCid 且无 prev）；
 * - 每个后续提交的 prev 指向前一提交；
 * - CID 不重复。
 */
function validateBranch(branch: Branch): void {
  const { genesisCid, commits } = branch;
  if (!commits || commits.length === 0) {
    throw new ConflictResolutionError(
      `分支 ${genesisCid} 的提交链为空`,
    );
  }
  const seen = new Set<string>();
  for (let index = 0; index < commits.length; index += 1) {
    const commit = commits[index];
    if (seen.has(commit.cid)) {
      throw new ConflictResolutionError(
        `分支 ${genesisCid} 中存在重复 CID: ${commit.cid}`,
      );
    }
    seen.add(commit.cid);

    if (index === 0) {
      if (commit.cid !== genesisCid) {
        throw new ConflictResolutionError(
          `分支首提交 ${commit.cid} 与创世 CID ${genesisCid} 不一致`,
        );
      }
      if (commit.prev !== undefined) {
        throw new ConflictResolutionError(
          `创世提交 ${genesisCid} 不应携带父提交引用`,
        );
      }
      continue;
    }

    const previous = commits[index - 1];
    if (commit.prev !== previous.cid) {
      throw new ConflictResolutionError(
        `提交 ${commit.cid} 的 prev(${commit.prev ?? 'undefined'}) 与前一提交 ${previous.cid} 不匹配`,
      );
    }
  }
}

/**
 * 运行调用方注入的锚定验证器。
 * 验证器抛异常、返回 undefined 或返回值缺少时间戳，均视为未通过验证，
 * 该锚定按“未锚定”处理。
 */
async function verifyAnchor(
  branch: Branch,
  commit: StreamCommit,
  verifier: AnchorVerifier,
): Promise<number | undefined> {
  if (commit.anchor === undefined) {
    return undefined;
  }
  try {
    const info = await verifier(commit.anchor, commit.cid, branch);
    if (info !== undefined && typeof info.anchorTimestamp === 'number') {
      return info.anchorTimestamp;
    }
    return undefined;
  } catch {
    return undefined;
  }
}

/** 校验结构并对链上所有锚定凭据逐一验证，取最后一个有效锚定作为该支锚定。 */
async function evaluateBranch(
  branch: Branch,
  verifier: AnchorVerifier,
): Promise<EvaluatedBranch> {
  validateBranch(branch);
  const evaluated: EvaluatedBranch = {
    branch,
    tipCid: branch.commits[branch.commits.length - 1].cid,
    length: branch.commits.length,
  };
  for (let index = 0; index < branch.commits.length; index += 1) {
    const commit = branch.commits[index];
    const anchorTimestamp = await verifyAnchor(branch, commit, verifier);
    if (anchorTimestamp !== undefined) {
      evaluated.anchorCommitIndex = index;
      evaluated.anchorTimestamp = anchorTimestamp;
    }
  }
  return evaluated;
}

/**
 * 计算分叉点：两支最后一个共有 CID 的下标。
 * 仅同一下标位置的 CID 相同才视为共有（结构校验已保证链内引用连续）。
 * 没有任何共有提交时返回 -1。
 */
function forkPointIndex(a: EvaluatedBranch, b: EvaluatedBranch): number {
  const sharedLength = Math.min(a.length, b.length);
  let forkIndex = -1;
  for (let index = 0; index < sharedLength; index += 1) {
    if (a.branch.commits[index].cid === b.branch.commits[index].cid) {
      forkIndex = index;
    } else {
      break;
    }
  }
  return forkIndex;
}

function isAnchored(evaluated: EvaluatedBranch): boolean {
  return evaluated.anchorTimestamp !== undefined;
}

function buildResult(
  winner: EvaluatedBranch,
  loser: EvaluatedBranch,
  reason: ResolutionReason,
): ResolutionResult {
  return {
    winner: winner.branch,
    loser: loser.branch,
    tipCid: winner.tipCid,
    tipLength: winner.length,
    anchorTimestamp: winner.anchorTimestamp,
    reason,
  };
}

/**
 * 分叉点不早于当前锚定提交时的通用比较规则（对称规则）：
 * 1. 已锚定一支优先于未锚定一支；
 * 2. 都已锚定时，锚定区块时间戳较早者胜，时间戳相同则较长者胜；
 * 3. 都未锚定时较长者胜；
 * 4. 长度也相同时提示 CID 字典序较小者胜。
 */
function compareGeneric(
  current: EvaluatedBranch,
  candidate: EvaluatedBranch,
): ResolutionResult {
  if (isAnchored(candidate) && !isAnchored(current)) {
    return buildResult(candidate, current, 'anchored-over-unanchored');
  }
  if (isAnchored(current) && !isAnchored(candidate)) {
    return buildResult(current, candidate, 'anchored-over-unanchored');
  }

  if (isAnchored(current) && isAnchored(candidate)) {
    const currentTimestamp = current.anchorTimestamp as number;
    const candidateTimestamp = candidate.anchorTimestamp as number;
    if (candidateTimestamp < currentTimestamp) {
      return buildResult(candidate, current, 'earlier-anchor-timestamp');
    }
    if (currentTimestamp < candidateTimestamp) {
      return buildResult(current, candidate, 'earlier-anchor-timestamp');
    }
  }

  if (candidate.length > current.length) {
    return buildResult(candidate, current, 'longer-history');
  }
  if (current.length > candidate.length) {
    return buildResult(current, candidate, 'longer-history');
  }

  if (candidate.tipCid < current.tipCid) {
    return buildResult(candidate, current, 'smaller-tip-cid');
  }
  return buildResult(current, candidate, 'smaller-tip-cid');
}

/**
 * 解决同一流上当前分支与候选分支之间的冲突，返回应当保留为当前提示的分支。
 *
 * 当候选分支的分叉点早于当前已锚定提交时，适用额外约束：
 * 候选分支必须凭自身有效锚定、且锚定时间严格早于当前锚定时间，才能取代当前分支；
 * 时间戳相同也不允许取代（防止用更长的未锚定/同时间历史回滚已锚定历史）。
 *
 * @param current 节点当前采用的分支
 * @param candidate 新收到的候选分支
 * @param verifier 调用方注入的锚定凭据验证器
 */
export async function resolveConflict(
  current: Branch,
  candidate: Branch,
  verifier: AnchorVerifier,
): Promise<ResolutionResult> {
  if (current.genesisCid !== candidate.genesisCid) {
    throw new ConflictResolutionError(
      `创世 CID 不一致，无法比较: ${current.genesisCid} vs ${candidate.genesisCid}`,
    );
  }

  const evaluatedCurrent = await evaluateBranch(current, verifier);
  const evaluatedCandidate = await evaluateBranch(candidate, verifier);

  if (evaluatedCurrent.tipCid === evaluatedCandidate.tipCid) {
    return buildResult(evaluatedCurrent, evaluatedCandidate, 'same-tip');
  }

  const forkIndex = forkPointIndex(evaluatedCurrent, evaluatedCandidate);
  const candidateForksBeforeCurrentAnchor =
    evaluatedCurrent.anchorCommitIndex !== undefined &&
    forkIndex < evaluatedCurrent.anchorCommitIndex;

  if (candidateForksBeforeCurrentAnchor) {
    if (!isAnchored(evaluatedCandidate)) {
      return buildResult(
        evaluatedCurrent,
        evaluatedCandidate,
        'unanchored-cannot-replace-anchored',
      );
    }
    const currentTimestamp = evaluatedCurrent.anchorTimestamp as number;
    const candidateTimestamp = evaluatedCandidate.anchorTimestamp as number;
    if (candidateTimestamp < currentTimestamp) {
      return buildResult(
        evaluatedCandidate,
        evaluatedCurrent,
        'anchor-before-current-anchor',
      );
    }
    return buildResult(
      evaluatedCurrent,
      evaluatedCandidate,
      'anchor-not-before-current-anchor',
    );
  }

  return compareGeneric(evaluatedCurrent, evaluatedCandidate);
}
