// Package netsec 提供供验证宿主调用的定时状态机能力。
//
// 建模约束（由 Machine.Validate 强制）：
//   - 至多 MaxClocks 只时钟（当前为 3）；
//   - 守卫只能是时钟与 [0, MaxConst] 内整数常数的比较；
//   - 迁移动作只能把时钟归零；
//   - 位置不变量只接受上界（< 或 <=）。
//
// 探索与交叉验证：
//   - Explore 按区域等价（region equivalence）枚举离散区域代表点，
//     搜索错误位置，并在可达时返回带每跳具体延迟的 Witness；
//   - ReferenceExplore 是独立的参考模型：在离散整数边界网格上，
//     用边界本身、±Eps 微小偏移与 0.5 分数代表做具体估值 BFS，
//     用于和符号引擎对拍严格与非严格边界；
//   - ProbeBoundaries 在 c-Eps、c、c+Eps 三点分别按严格/非严格
//     语义评估，暴露边界归属差异；
//   - Replay 独立复放见证，逐步核对延迟、不变量、守卫与归零。
//
// zone.go 的 DBM（差界定界矩阵）是独立可用的时区工具，
// 提供约束、时间流逝、归零与成员判定。
package netsec
