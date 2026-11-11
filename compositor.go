// Package compositor 提供设计稿渲染服务使用的图层合成功能。
//
// 所有像素运算都在预乘 alpha 下进行,与 W3C Compositing and Blending
// Level 1 规范及浏览器行为对齐:
//
//   - Porter-Duff 各合成算子,见 Op。
//   - W3C 混合模式(multiply、screen、overlay、soft-light、color-dodge
//     等全部可分离与不可分离模式),见 Mode。
//   - 隔离组、组透明度与裁剪遮罩,见 Group 与 Layer。
//
// 包内提供两条像素路径:8 位路径(Image8)在写入缓冲区时量化到
// 1/255,浮点路径(ImageF)不做量化。两条路径对同一像素流水线的
// 误差有界:每经过一次 8 位缓冲写入,逐通道误差最多增加 0.5/255。
package compositor
