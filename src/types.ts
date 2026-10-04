/** 学期类别（开课学期） */
export type Term = 'fall' | 'spring';

/**
 * 先修要求：
 * - string        —— 单门先修课
 * - { anyOf: [...] } —— 课程组替代：组内任选其一即可满足
 */
export type Prereq = string | { anyOf: string[] };

export interface Course {
  id: string;
  name: string;
  credits: number;
  /** 开课学期，如 ['fall'] 表示仅秋季开课 */
  offered: Term[];
  /** 先修：必须在更早学期完成（严格先于） */
  prereqs: Prereq[];
  /** 同修：同学期或更早修读即可 */
  coreqs: string[];
}

export interface PlannerInput {
  courses: Course[];
  /** 毕业必修课 */
  required: string[];
  /** 毕业总学分要求 */
  requiredCredits: number;
  /** 每学期最大学分负载 */
  maxCreditsPerSemester: number;
  /** 总课程门数上限（默认 22） */
  maxCourses?: number;
  /** 入学学期（第 1 学期的季节） */
  startTerm: Term;
  /** 允许搜索的最大学期数 */
  maxSemesters: number;
  /**
   * 可用于凑学分的选修池（课程 id）。
   * 默认：所有非必修、且无先修/同修的课程。
   */
  electives?: string[];
}

/** 一条被满足的替代路径：course 的某个课程组先修，选择了 chosen */
export interface SubstitutionChoice {
  forCourse: string;
  options: string[];
  chosen: string;
}

/** 无法通过同修解决的先修环 */
export interface PrereqCycle {
  /** 环上的课程（按依赖顺序） */
  courses: string[];
  /** 环中至少包含一条严格先修边，故无法靠同修同学期修读解决 */
  reason: string;
  /** 环是否涉及课程组替代边（若是，换一个替代选项可能消除该环） */
  involvesSubstitution: boolean;
}

export interface SemesterPlan {
  index: number; // 1 起
  term: Term;
  courses: string[];
  credits: number;
}

export interface PlanResult {
  feasible: boolean;
  /** 无法通过同修解决的先修环（总是返回，便于诊断） */
  cycles: PrereqCycle[];
  semesters: SemesterPlan[];
  substitutions: SubstitutionChoice[];
  totalCredits: number;
  /** 超出毕业要求的额外学分 */
  extraCredits: number;
  totalCourses: number;
  message?: string;
}
