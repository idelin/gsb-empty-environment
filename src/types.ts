/** 学期季节 */
export type Season = 'spring' | 'summer' | 'fall' | 'winter';

/**
 * 一条先修要求：多个替代方案取其一（OR），
 * 每个替代方案是一组必须全部完成的课程（AND，即课程组替代）。
 * 单课程先修写作 [[id]]。
 */
export interface PrereqRequirement {
  alternatives: string[][];
}

export interface Course {
  id: string;
  name: string;
  credits: number;
  /** 开课季节（不含年份，按学年循环） */
  offered: Season[];
  /** 先修要求列表，每条都必须在更早的学期满足 */
  prereqs?: PrereqRequirement[];
  /** 同修课程：同学期或更早修读即可 */
  coreqs?: string[];
}

export interface TermSpec {
  year: number;
  season: Season;
  maxCredits: number;
  maxCourses: number;
}

export interface PlannerConfig {
  courses: Course[];
  /** 毕业要求的课程 id */
  required: string[];
  /** 按时间顺序排列的候选学期 */
  terms: TermSpec[];
  /** 全程最多修读课程门数，默认 22 */
  maxTotalCourses?: number;
}

export interface SubstitutionChoice {
  /** 拥有该先修要求的课程 */
  forCourse: string;
  requirementIndex: number;
  /** 实际满足的替代路径（课程组） */
  chosen: string[];
}

export interface TermSchedule {
  spec: TermSpec;
  courses: Course[];
  credits: number;
}

export interface Plan {
  terms: TermSchedule[];
  totalCredits: number;
  /** 超出毕业要求课程学分总和的部分 */
  extraCredits: number;
  totalCourses: number;
  substitutions: SubstitutionChoice[];
}

export interface CycleEdge {
  from: string;
  to: string;
  requirementIndex: number;
}

/** 一个无法通过同修解决的先修环（强连通分量） */
export interface CycleReport {
  courses: string[];
  edges: CycleEdge[];
}

export type PlanResult =
  | { ok: true; plan: Plan; cycles: CycleReport[] }
  | { ok: false; reason: string; cycles: CycleReport[] };
