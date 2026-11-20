package admission

import "errors"

// 准入控制各阶段返回的哨兵错误，调用方可用 errors.Is 精确区分拒绝原因。
var (
	// ErrDigestLength 摘要十六进制解码后的长度非法。
	ErrDigestLength = errors.New("admission: digest has wrong length")
	// ErrNonceLength 实例随机数十六进制解码后的长度非法。
	ErrNonceLength = errors.New("admission: nonce has wrong length")

	// ErrPolicyVersion 策略版本为零或与宿主当前策略不匹配。
	ErrPolicyVersion = errors.New("admission: invalid policy version")
	// ErrRootClaimMissing 策略根声明在声明树中不存在。
	ErrRootClaimMissing = errors.New("admission: root claim missing")
	// ErrUnknownClaim 引用了声明树中不存在的声明。
	ErrUnknownClaim = errors.New("admission: unknown claim")
	// ErrDuplicateChild 同一节点的子声明列表出现重复项。
	ErrDuplicateChild = errors.New("admission: duplicate child claim")
	// ErrOrphanClaim 声明树包含从根不可达的孤立声明（多棵树）。
	ErrOrphanClaim = errors.New("admission: orphan claim in tree")

	// ErrBadSignature Ed25519 签名验证失败。
	ErrBadSignature = errors.New("admission: bad signature")
	// ErrBootMismatch 启动度量与根证据或策略不匹配。
	ErrBootMismatch = errors.New("admission: boot measurement mismatch")
	// ErrGenerationRollback 恢复代次没有严格单调递增。
	ErrGenerationRollback = errors.New("admission: generation rollback")
	// ErrStaleGeneration 票据/批次绑定的代次与宿主当前代次不一致。
	ErrStaleGeneration = errors.New("admission: stale generation")

	// ErrParentNotFound 票据父实例既不在已发布集合，也不在本批待创建集合。
	ErrParentNotFound = errors.New("admission: parent instance not found")
	// ErrInstanceExists 同名或同随机数实例已经发布，不能重复创建。
	ErrInstanceExists = errors.New("admission: instance already exists")
	// ErrUnauthorizedClaim 子声明不在父声明允许创建的子节点集合内。
	ErrUnauthorizedClaim = errors.New("admission: claim not allowed for parent")
	// ErrWrongPurpose 票据用途与操作不匹配。
	ErrWrongPurpose = errors.New("admission: ticket purpose mismatch")
	// ErrPermission 票据转授了父实例不具备的卷权限或超层委托。
	ErrPermission = errors.New("admission: delegation exceeds parent authority")
	// ErrBindingMismatch 票据绑定的随机数/声明摘要与实际创建项不一致。
	ErrBindingMismatch = errors.New("admission: ticket binding mismatch")
	// ErrCyclic 提交图中存在环形引用，无法构成无环实例树。
	ErrCyclic = errors.New("admission: cyclic instance reference")
	// ErrDisconnected 提交项无法从已认证根实例出发全部到达。
	ErrDisconnected = errors.New("admission: instance tree disconnected from root")
	// ErrCrossTree 票据来自另一棵合法实例树，禁止拼接父身份与卷权限。
	ErrCrossTree = errors.New("admission: ticket crosses instance trees")
	// ErrTicketConsumed 票据已被先前批次消费，禁止重复使用。
	ErrTicketConsumed = errors.New("admission: ticket already consumed")
	// ErrEmptyBatch 批次不包含任何创建请求。
	ErrEmptyBatch = errors.New("admission: empty batch")

	// ErrStoreConflict 比较并替换回调发现状态已被并发提交推进。
	ErrStoreConflict = errors.New("admission: store cas conflict")
	// ErrNotBootstrapped 宿主尚未建立信任锚就开始签出或提交。
	ErrNotBootstrapped = errors.New("admission: host not bootstrapped")

	// ErrSnapshotEmpty 快照缺少根、实例或收据。
	ErrSnapshotEmpty = errors.New("admission: empty snapshot")
	// ErrReceiptReplay 快照中多张收据消费了同一张票据。
	ErrReceiptReplay = errors.New("admission: receipt replays ticket")
	// ErrSnapshotCoverage 快照实例与收据重建出的实例集合不一致。
	ErrSnapshotCoverage = errors.New("admission: snapshot instance coverage mismatch")
)
