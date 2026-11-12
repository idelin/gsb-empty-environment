package pixelbudget

// 以空导入注册 PNG 与 JPEG 解码器，使 Decode 默认路径上的 image.Decode
// 能够识别本包 Inspect 支持的两种格式；嵌入方无需再自行空导入。
import (
	_ "image/jpeg"
	_ "image/png"
)
