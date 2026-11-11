// Package tablegrid 把已解析的 HTML 表格节点树展开为二维网格。
//
// 展开过程遵循 HTML 规范的表格模型（table model）：
//   - 按文档顺序处理 thead/tbody/tfoot 与裸 tr 的隐式 tbody；
//   - 按“逐行逐格找空槽位”的方式放置 td/th，处理 colspan、rowspan；
//   - rowspan=0 延伸到单元格所在行组的最后一行，不跨越行组边界；
//   - 单元格与已占用槽位重叠时插入新列并从头重走放置流程；
//   - 非正跨度回退为 1，超过 1000 的跨度钳制为 1000；
//   - 多行 th 表头合并为每列的列路径，每个单元格保留来源行列号。
//
// 输入只需实现 Node 接口，与具体的 HTML 解析器解耦。
package tablegrid
