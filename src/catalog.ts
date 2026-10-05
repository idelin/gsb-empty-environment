import type { Course, TermSpec } from './types.ts';

/** 示例课程目录：含先修、同修、课程组替代、按季节开课 */
export const catalog: Course[] = [
  { id: 'CS101', name: '程序设计基础', credits: 3, offered: ['fall', 'spring'] },
  { id: 'CS102', name: '数据结构', credits: 3, offered: ['fall', 'spring'], prereqs: [{ alternatives: [['CS101']] }] },
  { id: 'CS201', name: '算法设计与分析', credits: 3, offered: ['fall'], prereqs: [{ alternatives: [['CS102']] }] },
  { id: 'CS210', name: '计算机组成原理', credits: 4, offered: ['spring'], prereqs: [{ alternatives: [['CS102']] }] },
  { id: 'CS215', name: '数字逻辑', credits: 3, offered: ['spring'], prereqs: [{ alternatives: [['CS101']] }] },
  { id: 'CS216', name: '汇编语言', credits: 3, offered: ['spring'], prereqs: [{ alternatives: [['CS101']] }] },
  { id: 'CS220', name: '离散数学', credits: 3, offered: ['fall', 'spring'] },
  { id: 'MATH101', name: '微积分', credits: 4, offered: ['fall', 'spring'] },
  { id: 'MATH102', name: '线性代数', credits: 3, offered: ['fall', 'spring'], prereqs: [{ alternatives: [['MATH101']] }] },
  {
    id: 'CS310',
    name: '操作系统',
    credits: 4,
    offered: ['fall'],
    prereqs: [
      { alternatives: [['CS201']] },
      // 课程组替代：CS210，或 CS215+CS216 组合
      { alternatives: [['CS210'], ['CS215', 'CS216']] },
    ],
  },
  { id: 'CS320', name: '数据库系统', credits: 3, offered: ['spring'], prereqs: [{ alternatives: [['CS201']] }] },
  {
    id: 'CS410',
    name: '编译原理',
    credits: 4,
    offered: ['fall'],
    prereqs: [{ alternatives: [['CS310']] }],
    coreqs: ['CS415'],
  },
  { id: 'CS415', name: '编译实验', credits: 1, offered: ['fall'], coreqs: ['CS410'] },
  {
    id: 'CS499',
    name: '毕业设计',
    credits: 6,
    offered: ['fall', 'spring'],
    prereqs: [{ alternatives: [['CS310']] }, { alternatives: [['CS320']] }],
  },
  // 无法通过同修解决的先修环（演示用，不属于毕业要求）
  { id: 'XX401', name: '高级专题A', credits: 3, offered: ['fall'], prereqs: [{ alternatives: [['XX402']] }] },
  { id: 'XX402', name: '高级专题B', credits: 3, offered: ['spring'], prereqs: [{ alternatives: [['XX401']] }] },
];

export const requiredCourses: string[] = [
  'CS101', 'CS102', 'CS201', 'CS220', 'MATH101', 'MATH102',
  'CS310', 'CS320', 'CS410', 'CS415', 'CS499',
];

/** 生成 4 学年（秋/春）候选学期，每学期 18 学分、5 门课上限 */
export function makeTerms(startYear: number, years: number): TermSpec[] {
  const terms: TermSpec[] = [];
  for (let y = 0; y < years; y += 1) {
    terms.push({ year: startYear + y, season: 'fall', maxCredits: 18, maxCourses: 5 });
    terms.push({ year: startYear + y, season: 'spring', maxCredits: 18, maxCourses: 5 });
  }
  return terms;
}
