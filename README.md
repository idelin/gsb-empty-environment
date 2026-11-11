# compositor

设计稿渲染服务的图层混合结果和浏览器对不上。给Go渲染服务实现供页面渲染调用的图层合成包：在预乘alpha下实现Porter-Duff各算子，以及W3C定义的multiply、screen、overlay、soft-light、color-dodge等混合模式，支持隔离组、组透明度和裁剪遮罩；8位与浮点两条路径的误差有界。按规范公式逐像素生成参考，覆盖全透明、半透明和溢出边界。
