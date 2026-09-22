package readindexsim

type Role int

const (
	Follower Role = iota
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Candidate:
		return "candidate"
	case Leader:
		return "leader"
	default:
		return "follower"
	}
}

type Op struct {
	Kind  string
	Key   string
	Value string
}

type Entry struct {
	Index int
	Term  int
	Op    Op
}

type MessageType int

const (
	RequestVote MessageType = iota
	VoteResult
	AppendEntries
	AppendResult
)

type Message struct {
	From         int
	To           int
	Type         MessageType
	Term         int
	ReadID       int
	Granted      bool
	Success      bool
	Entries      []Entry
	PrevLogIndex int
	PrevLogTerm  int
	LeaderCommit int
	MatchIndex   int
}

type ReadRequest struct {
	ID  int
	Key string
}

type ReadEvidence struct {
	LeaderID     int
	Term         int
	ReadIndex    int
	Acks         []int
	AppliedIndex int
}

type ReadResult struct {
	ReadID   int
	LeaderID int
	Key      string
	Value    string
	Evidence ReadEvidence
}

type ReadState struct {
	id        int
	key       string
	term      int
	readIndex int
	acks      map[int]bool
}

type AuditEvent struct {
	Tick    int64
	Node    int
	Message string
}

type Node struct {
	id              int
	role            Role
	term            int
	votedFor        int
	leaderID        int
	log             []Entry
	commitIndex     int
	appliedIndex    int
	state           map[string]string
	electionTimeout int
	electionGen     int
	heartbeatTick   int
	nextIndex       map[int]int
	matchIndex      map[int]int
	votes           map[int]bool
	reads           map[int]*ReadState
	applyPaused     bool
}
