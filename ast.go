package netsec

// TermKind 区分 Datalog 词项的具体种类。
type TermKind int

const (
	// TermVariable 是形如 $name 的变量。
	TermVariable TermKind = iota
	// TermSymbol 是裸标识符常量，例如 read、file1。
	TermSymbol
	// TermString 是双引号字符串常量。
	TermString
	// TermInteger 是整数字面量常量。
	TermInteger
)

// Term 是谓词参数位置上的一个词项，可以是变量或常量。
type Term struct {
	Kind  TermKind
	Value string
}

// Var 构造一个变量词项，name 不含前导 $。
func Var(name string) Term { return Term{Kind: TermVariable, Value: name} }

// Symbol 构造一个裸标识符常量词项。
func Symbol(value string) Term { return Term{Kind: TermSymbol, Value: value} }

// String 构造一个字符串常量词项。
func String(value string) Term { return Term{Kind: TermString, Value: value} }

// Integer 构造一个整数字面量常量词项，value 为十进制文本。
func Integer(value string) Term { return Term{Kind: TermInteger, Value: value} }

// IsVariable 报告该词项是否为变量。
func (t Term) IsVariable() bool { return t.Kind == TermVariable }

// IsGround 报告该词项是否为常量（不含变量）。
func (t Term) IsGround() bool { return t.Kind != TermVariable }

// Predicate 是一个具名、带固定元数的词项序列，可出现在事实、规则头部与规则体中。
type Predicate struct {
	Name string
	Args []Term
}

// IsGround 报告谓词的所有参数是否都是常量。
func (p Predicate) IsGround() bool {
	for _, arg := range p.Args {
		if arg.IsVariable() {
			return false
		}
	}
	return true
}

// Rule 表示 head :- body 形式的 Datalog 规则。
type Rule struct {
	Head Predicate
	Body []Predicate
}

// Check 表示 check if body 形式的查询式校验，body 至少能推出一组绑定即通过。
type Check struct {
	Body []Predicate
}

// PolicyKind 区分服务端策略的种类。
type PolicyKind int

const (
	// PolicyAllow 是 allow if 策略。
	PolicyAllow PolicyKind = iota
	// PolicyDeny 是 deny if 策略。
	PolicyDeny
)

// Policy 表示 allow if / deny if 形式的服务端策略。
type Policy struct {
	Kind PolicyKind
	Body []Predicate
}

// Block 是一个 Biscuit 块。索引 0 是 authority 块，其余是衰减块。
type Block struct {
	// Index 是块在令牌中的位置，从 0 开始；0 表示 authority 块。
	Index int
	// Facts 是块中直接声明的 ground 事实。
	Facts []Predicate
	// Rules 是块中声明的规则，其推导结果始终留在本块作用域内。
	Rules []Rule
	// Checks 是块中声明的 check if 校验。
	Checks []Check
}

// IsAuthority 报告该块是否为 authority 块（索引 0）。
func (b Block) IsAuthority() bool { return b.Index == 0 }

// Token 是解析后的 Biscuit 令牌，按顺序持有全部块。
type Token struct {
	Blocks []Block
}

// Authorizer 是服务端授权器，持有服务端策略、服务端事实与运行期环境事实。
type Authorizer struct {
	// Policies 按书写顺序保存 allow/deny 策略，评估时第一条匹配者决定。
	Policies []Policy
	// Facts 是服务端自有事实，仅进入策略评估的事实集合。
	Facts []Predicate
	// Ambient 是运行期环境事实（如当前时间、请求资源），仅进入策略评估。
	Ambient []Predicate
}

// Decision 是最终授权决定。
type Decision int

const (
	// DecisionDeny 表示拒绝。
	DecisionDeny Decision = iota
	// DecisionAllow 表示允许。
	DecisionAllow
)

// Result 是一次令牌信任评估的结构化结果。
type Result struct {
	// Decision 是最终决定；出错或无策略匹配时为 DecisionDeny。
	Decision Decision
	// MatchedPolicy 是命中的服务端策略下标；未命中时为 -1。
	MatchedPolicy int
	// Block 是出错块的索引；策略阶段或输入阶段出错时为 -1。
	Block int
	// Reason 是拒绝或出错的具体错误；成功允许时为 nil。
	Reason error
}

// Allowed 报告评估结果是否为允许。
func (r Result) Allowed() bool { return r.Decision == DecisionAllow }
