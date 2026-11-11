// Package polyglot 按文件自身的结构规则判定上传内容的真实类型，
// 用于抵御多义文件（polyglot）绕过基于扩展名或首个魔数的内容策略。
//
// 判定覆盖 PNG、GIF、PDF、ZIP 四种格式。每种格式都有独立的结构校验器：
//   - PNG：校验 8 字节签名与块链，要求 IHDR 为首个块、IEND 为最后一个块，
//     IEND 之后不允许任何尾随字节。
//   - GIF：校验版本头、逻辑屏幕描述符、图像/扩展块与块终止符 0x3B，
//     终止符之后不允许任何尾随字节，因此 GIF 后追加 JavaScript 会被判为无效。
//   - PDF：允许前置垃圾字节，定位 %PDF- 头、startxref、xref/trailer 与 %%EOF。
//   - ZIP：从文件尾部扫描 End Of Central Directory，沿中央目录逐个核对文件头。
//
// 当同一份内容能被两种及以上格式成功解析时，Detect 返回 ErrAmbiguous，
// 拒绝原因中列出全部命中格式；没有任何格式能解析时返回 ErrUnknown。
package polyglot
