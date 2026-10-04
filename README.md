# conv

Go 整数卷积库：对整数图像做二维离散卷积（相关运算，锚点约定与 OpenCV `filter2D` 一致）。

## 特性

- **边界模式**：零填充（`BoundaryZero`）、镜像（`BoundaryMirror`，半采样对称，边缘像素重复）、循环（`BoundaryCircular`）。
- **任意锚点**：核锚点不必在中心，甚至可以位于核之外。
- **可分离加速**：`Convolve` 自动检测精确可分离（整数意义下秩 1）的核并走两次一维卷积的快路径；只有精确可分离才走快路径，否则回退直接卷积。`ConvolveDirect` 始终使用直接算法。
- **大整数累加**：全部中间结果用 `math/big` 累加，核系数与像素为 `int64`，任意大小都不会中间溢出。
- 覆盖偶数尺寸核、单行/单列图像、负系数等情形。

## API

```go
type Kernel struct {
    Coeffs  [][]int64 // Coeffs[y][x]
    AnchorX int
    AnchorY int
}

func Convolve(img [][]int64, k Kernel, mode BoundaryMode) [][]*big.Int
func ConvolveDirect(img [][]int64, k Kernel, mode BoundaryMode) [][]*big.Int
func (k Kernel) Separable() bool
```

输出定义：`out[y][x] = Σ K[ky][kx] * img[y+ky-AnchorY][x+kx-AnchorX]`，越界像素按边界模式处理。

## 测试

```sh
go test ./...
```
