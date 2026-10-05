/** 积分规则（默认采用 FIBA 风格：胜 2 分、负 1 分、弃权负 0 分） */
export interface Scoring {
  /** 正常获胜得分 */
  win: number;
  /** 正常落败得分 */
  loss: number;
  /** 平局双方各得分 */
  draw: number;
  /** 对手弃权时获胜方得分 */
  forfeitWin: number;
  /** 弃权方得分 */
  forfeitLoss: number;
}

export const DEFAULT_SCORING: Scoring = {
  win: 2,
  loss: 1,
  draw: 1,
  forfeitWin: 2,
  forfeitLoss: 0,
};

/** 一场比赛的结果：要么给出比分，要么标记弃权方 */
export type MatchResult =
  | { type: 'score'; home: string; away: string; homeScore: number; awayScore: number }
  | { type: 'forfeit'; home: string; away: string; forfeitedBy: 'home' | 'away' };

/** 一个名次条目；teams 长度大于 1 表示并列 */
export interface Standing {
  rank: number;
  teams: string[];
  /** 该队（或并列各队）的总积分 */
  points: number;
}

/** 解释树节点：记录每一步决胜的依据 */
export interface ExplanationNode {
  kind: 'points' | 'head-to-head' | 'resolved' | 'tied';
  /** 人类可读的说明文字 */
  summary: string;
  /** 本节点涉及的队伍 */
  teams: string[];
  /** 本节点比较时各队的积分（总积分或相互战绩积分） */
  points?: Record<string, number>;
  /** 仅 resolved / tied 节点有值：确定下来的名次 */
  rank?: number;
  children: ExplanationNode[];
}

export interface RankingResult {
  /** 完整排名，按名次升序；并列队伍共享同一条目 */
  standings: Standing[];
  /** 解释树，根节点为总积分排名 */
  explanation: ExplanationNode;
}
