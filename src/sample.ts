import type { Course, PlannerInput } from './types.ts';

/** 示例计算机科学课程体系 */
export const sampleCourses: Course[] = [
  { id: 'CS101', name: '程序设计基础', credits: 4, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
  { id: 'MATH1', name: '微积分I', credits: 4, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
  { id: 'MATH2', name: '微积分II', credits: 4, offered: ['fall', 'spring'], prereqs: ['MATH1'], coreqs: [] },
  { id: 'CS102', name: '数据结构', credits: 4, offered: ['fall', 'spring'], prereqs: ['CS101'], coreqs: [] },
  { id: 'MATH3', name: '线性代数', credits: 3, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
  { id: 'CS201', name: '离散数学', credits: 3, offered: ['fall'], prereqs: ['CS101'], coreqs: [] },
  { id: 'CS210', name: '计算机组成', credits: 4, offered: ['spring'], prereqs: ['CS102'], coreqs: ['CS210L'] },
  { id: 'CS210L', name: '组成实验', credits: 1, offered: ['spring'], prereqs: [], coreqs: [] },
  { id: 'CS220', name: '算法分析', credits: 3, offered: ['fall'], prereqs: ['CS102', 'CS201'], coreqs: [] },
  // 课程组替代：数据库 / 网络 任选其一作为“系统方向基础”
  { id: 'CS301', name: '数据库系统', credits: 3, offered: ['fall'], prereqs: ['CS102'], coreqs: [] },
  { id: 'CS302', name: '计算机网络', credits: 3, offered: ['spring'], prereqs: ['CS102'], coreqs: [] },
  { id: 'CS310', name: '操作系统', credits: 4, offered: ['fall', 'spring'], prereqs: ['CS210'], coreqs: [] },
  {
    id: 'CS320',
    name: '软件工程',
    credits: 3,
    offered: ['spring'],
    prereqs: [{ anyOf: ['CS301', 'CS302'] }],
    coreqs: [],
  },
  { id: 'CS400', name: '编译原理', credits: 4, offered: ['fall'], prereqs: ['CS220', 'CS310'], coreqs: [] },
  {
    id: 'CS410',
    name: '分布式系统',
    credits: 3,
    offered: ['spring'],
    prereqs: ['CS310', { anyOf: ['CS301', 'CS302'] }],
    coreqs: [],
  },
  { id: 'CS490', name: '毕业设计', credits: 6, offered: ['fall', 'spring'], prereqs: ['CS320', 'CS400'], coreqs: [] },
  // 选修池（无先修，用于凑毕业学分）
  { id: 'HUM1', name: '科技写作', credits: 2, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
  { id: 'HUM2', name: '工程伦理', credits: 2, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
  { id: 'ART1', name: '设计思维', credits: 3, offered: ['spring'], prereqs: [], coreqs: [] },
  { id: 'PE1', name: '体育', credits: 1, offered: ['fall', 'spring'], prereqs: [], coreqs: [] },
];

export const sampleInput: PlannerInput = {
  courses: sampleCourses,
  required: [
    'CS101', 'MATH1', 'MATH2', 'CS102', 'MATH3', 'CS201', 'CS210',
    'CS220', 'CS310', 'CS320', 'CS400', 'CS410', 'CS490',
  ],
  requiredCredits: 56,
  maxCreditsPerSemester: 18,
  maxCourses: 22,
  startTerm: 'fall',
  maxSemesters: 10,
};
