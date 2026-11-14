# 扫描作业读到了其他用户的目录

网络扫描服务按作业标识取图像，攻击者枚举作业号读取他人扫描件。请实现供Go扫描服务调用的可导入作业访问组件。作业标识不可预测并绑定用户、设备与时间，读取前核对主体与作业归属；枚举、越权与过期拒绝。图像元数据剥离位置信息。用作业枚举、跨用户读取、过期作业、合法扫描和元数据验证只返回本用户扫描件。

## 使用

```go
import (
	"context"
	"time"

	"example.org/netsec/q0396"
)

ctx := context.Background()

// Subject 必须来自认证中间件，不能取自请求参数。
subject := netsec.Subject{UserID: "user-1", DeviceID: "scanner-7"}
access := netsec.NewAccess(netsec.WithDefaultTTL(5 * time.Minute))

job, err := access.CreateJob(ctx, netsec.JobSpec{
	Subject:     subject,
	Image:       jpegBytes,
	ContentType: "image/jpeg",
	ExpiresAt:   time.Now().Add(5 * time.Minute), // 可省略，使用默认 TTL
})
if err != nil {
	return err
}

scan, err := access.Read(ctx, subject, job.ID)
if err != nil {
	// errors.Is(err, netsec.ErrForbidden)：用户或设备不匹配
	// errors.Is(err, netsec.ErrExpired)：作业已过期
	// errors.Is(err, netsec.ErrRateLimited)：枚举触发限流
	return err
}
use(scan.Data)
```

## 安全约束

- `CreateJob` 使用 `crypto/rand` 生成 256 位 URL-safe 随机标识；标识中不包含用户、设备或时间信息，绑定关系保存在访问控制记录中。
- `Read` 在读取图像前同时核对 `UserID`、`DeviceID` 与绝对过期时间。不存在、跨用户和跨设备的错误同时匹配 `ErrNotFound` 与 `ErrForbidden`，避免泄露作业是否存在。
- 每个已认证用户在一分钟窗口内累计失败查询达到阈值后，后续查询返回 `ErrRateLimited`。窗口和阈值可通过 `WithFailureLimit` 调整，分布式部署应把限流状态放入共享存储。
- JPEG、PNG、WebP 和 GIF 在入库前和返回前各清洗一次。JPEG 删除 EXIF/XMP/IPTC/注释及非结构化 APP 段；PNG 删除 eXIf 和文本块；WebP 删除 EXIF/XMP 块；GIF 删除注释、XMP 和未知应用扩展。无法安全清洗的格式会被拒绝，而不是原样返回。
- 返回的 `Scan.Data` 是独立副本，调用方修改不会影响存储。

## 后端

默认使用 `MemoryStore`。可传入实现 `ScanStore` 的持久化后端：

```go
dir, err := netsec.NewDirStore("/var/lib/scanner/jobs")
if err != nil {
	return err
}
access := netsec.NewWithStore(dir)
```

`DirStore` 使用 0700 目录、0600 文件和原子重命名，并再次校验标识格式以阻止路径穿越。

## 验证

```sh
go test -race ./...
go vet ./...
```
