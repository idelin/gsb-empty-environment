// Package netsec 枚举弱内存序下的并发执行，用于说明普通读写是否可能读到
// 尚未发布的秘密缓冲区。
//
// 程序规模限制为最多 MaxThreads 个线程、每线程最多 MaxEventsPerThread 个
// 读写或屏障事件。宿主给定程序、读自边（ReadFrom）与允许重排关系
// （Pair），枚举器：

//   - 构建必须满足的先行发生（happens-before）偏序，包含线程程序序、读自边
//     以及发布/获取（含屏障）产生的同步边；
//   - 枚举该偏序的线性序，并按每位置写序（读连贯性）过滤非法交错；
//   - 在合法执行上用断言谓词寻找失败的最短见证（ShortestWitness）。
//
// ReferenceEnumerate 是不做剪枝的全排列穷举参考实现，用于与主枚举器交叉验证。
package netsec
