package netsec

// FailurePolicy 对应 ValidatingAdmissionPolicy.spec.failurePolicy。
type FailurePolicy string

const (
	// FailurePolicyFail 是 Kubernetes 的默认值，表达式求值失败时拒绝请求。
	FailurePolicyFail FailurePolicy = "Fail"
	// FailurePolicyIgnore 表示表达式求值失败时放行请求。
	FailurePolicyIgnore FailurePolicy = "Ignore"
)

// ParamNotFoundAction 对应 binding 的 parameterNotFoundAction。
type ParamNotFoundAction string

const (
	// ParamNotFoundDeny 是 Kubernetes 的默认值，参数缺失时拒绝请求。
	ParamNotFoundDeny ParamNotFoundAction = "Deny"
	// ParamNotFoundAllow 表示参数缺失时放行请求，会使策略失效。
	ParamNotFoundAllow ParamNotFoundAction = "Allow"
)

// ValidationAction 对应 binding 的 validationActions 条目。
type ValidationAction string

const (
	ActionDeny  ValidationAction = "Deny"
	ActionWarn  ValidationAction = "Warn"
	ActionAudit ValidationAction = "Audit"
)

// FindingCode 是失效路径的稳定标识，调用方应基于 code 而非消息文本做判断。
type FindingCode string

const (
	// CodeNameAndSelectorBothSet：paramRef 同时设置 name 与 selector，属非法配置，保存时必须拒绝。
	CodeNameAndSelectorBothSet FindingCode = "ParamRefNameAndSelectorBothSet"
	// CodeParameterMissingAllow：name 引用的参数对象当前不存在且 parameterNotFoundAction=Allow。
	CodeParameterMissingAllow FindingCode = "ParameterMissingAllow"
	// CodeSelectorZeroMatchAllow：selector 当前命中零个参数对象且 parameterNotFoundAction=Allow。
	CodeSelectorZeroMatchAllow FindingCode = "SelectorZeroMatchAllow"
	// CodeIgnoreExpressionError：failurePolicy=Ignore 且存在静态标记为可能出错的表达式或匹配条件。
	CodeIgnoreExpressionError FindingCode = "IgnoreExpressionError"
	// CodeNoDenyAction：validationActions 不含 Deny，违规请求只会被告警/审计而不会被拦截。
	CodeNoDenyAction FindingCode = "ValidationActionsWithoutDeny"
	// CodeParameterDeletable：参数对象可被受限主体删除，配合 Allow 构成绕过路径，配合 Deny 构成可用性风险。
	CodeParameterDeletable FindingCode = "ParameterDeletableByRestrictedSubject"
)

// Severity 表示发现项的严重级别。
type Severity string

const (
	SeverityBlocker Severity = "Blocker"
	SeverityHigh    Severity = "High"
	SeverityMedium  Severity = "Medium"
)

// Expression 描述一条 CEL 校验表达式及其静态出错标记。
type Expression struct {
	// RuleIndex 是表达式在 policy.validations 中的下标，用于报告定位。
	RuleIndex int
	// ErrorPossible 表示静态分析认为该表达式在某些输入下可能求值出错。
	ErrorPossible bool
}

// MatchCondition 描述一个匹配条件及其静态出错标记。
type MatchCondition struct {
	Name          string
	ErrorPossible bool
}

// Policy 是待保存的 ValidatingAdmissionPolicy 的分析输入。
type Policy struct {
	Name            string
	FailurePolicy   FailurePolicy
	Validations     []Expression
	MatchConditions []MatchCondition
}

// LabelSelectorRequirement 对应 metav1.LabelSelectorRequirement。
type LabelSelectorRequirement struct {
	Key      string
	Operator string // In、NotIn、Exists、DoesNotExist
	Values   []string
}

// LabelSelector 是 paramRef.selector 的简化模型。
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []LabelSelectorRequirement
}

// ParamRef 对应 binding 的 paramRef。
type ParamRef struct {
	// Name 引用单个参数对象。
	Name string
	// Selector 按标签选择参数对象；nil 表示未使用 selector。
	Selector *LabelSelector
}

// Binding 是待保存的 ValidatingAdmissionPolicyBinding 的分析输入。
type Binding struct {
	Name                    string
	ParamRef                *ParamRef
	ParameterNotFoundAction ParamNotFoundAction
	ValidationActions       []ValidationAction
}

// ParameterObject 是参数资源当前已存在的对象。
type ParameterObject struct {
	Name   string
	Labels map[string]string
}

// Subject 表示一个集群主体，Restricted 为 true 表示其不应具备参数对象的删除权。
type Subject struct {
	Name       string
	Restricted bool
}

// DeleteGrant 表示主体对某个参数对象拥有删除权。
// ObjectName 为 "*" 时表示对参数资源下任意对象（含未来新建对象）拥有删除权。
type DeleteGrant struct {
	Subject    Subject
	ObjectName string
}

// Finding 是一条失效路径分析发现。
type Finding struct {
	Code        FindingCode
	Severity    Severity
	Message     string
	PolicyName  string
	BindingName string
	// Bypassable 为 true 表示该发现项对应一条可让请求被放行的绕过路径。
	Bypassable bool
	// References 定位相关表达式，如 validation[0]、matchCondition:foo。
	References []string
	// RestrictedSubjects 列出可利用该路径的受限主体。
	RestrictedSubjects []string
}

// Report 是一次分析的全部发现。
type Report struct {
	Policy   Policy
	Binding  Binding
	Findings []Finding
}

// Rejected 表示配置本身非法，保存流程必须拒绝（目前仅 name 与 selector 同时设置）。
func (r Report) Rejected() bool {
	for _, finding := range r.Findings {
		if finding.Severity == SeverityBlocker {
			return true
		}
	}
	return false
}

// Bypassable 表示报告中至少存在一条请求放行绕过路径。
func (r Report) Bypassable() bool {
	for _, finding := range r.Findings {
		if finding.Bypassable {
			return true
		}
	}
	return false
}

// Has 报告是否包含指定 code 的发现项。
func (r Report) Has(code FindingCode) bool {
	for _, finding := range r.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}
