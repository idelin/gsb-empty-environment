package mvrewrite

// Checker bundles a catalog and reusable analysis/search options.
type Checker struct {
	Catalog *Catalog
	Options AnalyzeOptions
}

// New creates a Checker with default finite-search options.
func New(catalog *Catalog) *Checker {
	return &Checker{Catalog: catalog, Options: DefaultAnalyzeOptions()}
}

// WithOptions replaces analysis options and returns the checker.
func (c *Checker) WithOptions(opts AnalyzeOptions) *Checker {
	c.Options = opts
	return c
}

// Check analyzes all views against q.
func (c *Checker) Check(q *Query, views ...*View) (*Report, error) {
	catalog := c.Catalog
	if catalog == nil && q != nil {
		catalog = q.Schema
	}
	return AnalyzeWithOptions(catalog, q, views, c.Options)
}

// Decide is a convenience wrapper for Check that returns decisions directly.
func (c *Checker) Decide(q *Query, views ...*View) []Decision {
	rep, err := c.Check(q, views...)
	if err != nil {
		return []Decision{{Rewritable: false, Code: ReasonInvalidSpec, Reason: err.Error()}}
	}
	return rep.Decisions
}

// MatView is an alias for callers using the materialized-view terminology.
type MatView = View

// Result is an alias for callers using the decision-result terminology.
type Result = Decision
