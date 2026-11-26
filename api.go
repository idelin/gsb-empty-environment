package bgpsim

import "errors"

var errNilConfig = errors.New("bgpsim: nil Config")

// Run is a short alias for Simulate, the main entry point of the package.
func Run(cfg *Config) (*Result, error) {
	return Simulate(cfg)
}

// Validate checks node, cluster, route and IGP references without running
// the simulation. It is useful for service-side request admission.
func (c *Config) Validate() error {
	if c == nil {
		return errNilConfig
	}
	nodes := c.nodeByID()
	_, rrCluster, err := c.buildPeers(nodes)
	if err != nil {
		return err
	}
	prefixSet := map[string]bool{}
	for _, r := range c.ExternalRoutes {
		prefixSet[r.Prefix] = true
	}
	prefixes := make([]string, 0, len(prefixSet))
	for p := range prefixSet {
		prefixes = append(prefixes, p)
	}
	return c.validate(nodes, rrCluster, prefixes)
}

// Simulator is a thin stateful façade around Simulate for services that
// prefer a constructed component. The current engine is deterministic and
// side-effect free; Run can be called repeatedly.
type Simulator struct {
	cfg *Config
}

// NewSimulator validates cfg and returns a reusable Simulator.
func NewSimulator(cfg *Config) (*Simulator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Simulator{cfg: cfg}, nil
}

// Run executes the synchronous simulation.
func (s *Simulator) Run() (*Result, error) {
	return Simulate(s.cfg)
}
