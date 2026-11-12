package coververify

// Digest 是状态、输入或输出的内容摘要。推荐使用十六进制编码的密码学哈希。
type Digest string

// Receipt 是插件上报的单次状态转换回执。
//
// 回执不允许自报后继状态：后继状态只能由宿主的确定性转换回调重放得出，以防插件
// 仅凭声明伪造覆盖。
type Receipt struct {
	// ID 是可选的宿主侧回执标识；为空时由四要素内容派生确定性 UID。
	ID string

	// ModelVersion 是产生该转换的模型版本，链的可达性按模型版本分别核算。
	ModelVersion string

	// PrevState 是转换前状态摘要。
	PrevState Digest

	// Input 是插件实际喂给模型的输入摘要。
	Input Digest

	// Output 是插件观测到的输出摘要，必须与回调重放输出逐字节一致。
	Output Digest
}

// ReplayResult 是确定性转换回调 [OracleFunc] 的重放结果。
type ReplayResult struct {
	// NextState 是重放得到的后继状态摘要；拒绝转换时允许等于前状态。
	NextState Digest

	// Output 是重放得到的输出摘要，用于与回执比对。
	Output Digest

	// Rejected 标识这是否为一次拒绝转换。拒绝边同样是必须显式覆盖的转换。
	Rejected bool

	// Err 在回调无法完成重放（未知状态/输入、内部错误、panic 等）时设置。
	// 设置 Err 的转换不会计入覆盖，且结果不被缓存，回调修复后可重新核验。
	Err error
}

// OracleFunc 是宿主提供的确定性转换回调。
//
// 对相同的 (modelVersion, prev, input) 必须始终返回相同的成功结果；核验器会缓存
// 成功结果，保证同一条转换边在任意多份回执下得到唯一裁决。回调实现不得回调
// [Verifier] 的方法（核验在锁内进行）。
type OracleFunc func(modelVersion string, prev, input Digest) ReplayResult

// Status 是回执的核验状态。
type Status string

const (
	// StatusPending 表示断链：前状态不在该模型版本的已核验链上。回执保留待定，
	// 链补齐并重新核验后可能转为 [StatusCovered]。
	StatusPending Status = "pending"

	// StatusCovered 表示前状态可达且重放输出一致，转换计入覆盖。
	StatusCovered Status = "covered"

	// StatusMismatch 表示回调重放输出与回执输出不一致，不计覆盖。
	StatusMismatch Status = "mismatch"

	// StatusConflict 表示同一转换边存在互异输出回执，整条边隔离，不计覆盖。
	StatusConflict Status = "conflict"

	// StatusErrored 表示转换回调返回错误或 panic，不计覆盖，可在回调修复后重试。
	StatusErrored Status = "errored"

	// StatusInvalid 表示回执四要素缺失或显式 ID 被不同内容复用，不予受理。
	StatusInvalid Status = "invalid"
)

// TransitionKey 唯一标识一条转换边：同一模型版本下，从前状态经特定输入触发的转换。
type TransitionKey struct {
	ModelVersion string
	PrevState    Digest
	Input        Digest
}
