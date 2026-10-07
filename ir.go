package flowsec

// Instr is one node of the script IR. Programs are structured
// (no gotos); loops must be lowered by a frontend before analysis.
type Instr interface{ instrID() int }

// Input introduces an externally supplied value with an initial label.
type Input struct {
	ID        int
	Name      string
	Level     int
	Receivers []string // empty = unrestricted
}

func (i *Input) instrID() int { return i.ID }

// Const introduces a public constant (bottom label).
type Const struct {
	ID   int
	Name string
}

func (c *Const) instrID() int { return c.ID }

// Assign copies/merges values: Dst gets the join of its sources' labels
// plus the current control-dependency (pc) label.
type Assign struct {
	ID   int
	Dst  string
	Srcs []string
}

func (a *Assign) instrID() int { return a.ID }

// If executes Then or Else under a pc label derived from Cond.
// The pc dependency is baked into labels of anything assigned inside
// and is NOT dropped when the branch joins.
type If struct {
	ID   int
	Cond string
	Then []Instr
	Else []Instr
}

func (i *If) instrID() int { return i.ID }

// Declassify is the ONLY construct that can shrink a label, and only
// when the program's Policy carries a matching grant for this node.
type Declassify struct {
	ID           int
	Dst          string
	Src          string
	ToLevel      int
	AddReceivers []string
	Token        string
}

func (d *Declassify) instrID() int { return d.ID }

// Sink is an observation point: Src flows to Receiver, who is allowed
// to observe levels up to MaxLevel.
type Sink struct {
	ID       int
	Name     string
	Src      string
	Receiver string
	MaxLevel int
}

func (s *Sink) instrID() int { return s.ID }

// Grant authorizes exactly one declassification target at one node.
type Grant struct {
	Token        string
	ToLevel      int
	AddReceivers []string
}

// Policy maps declassify instruction id -> grant.
type Policy struct {
	Grants map[int]Grant
}

// Program is a script lowered to the IR plus its receiver universe
// and declassification policy.
type Program struct {
	Receivers []string
	Instrs    []Instr
	Policy    Policy
}

func subsetOf(a, b []string) bool {
	if len(a) == 0 {
		return true
	}
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	for _, x := range a {
		if !set[x] {
			return false
		}
	}
	return true
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
