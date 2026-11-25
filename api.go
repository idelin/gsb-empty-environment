package evidence

// Decision and Result are aliases for Bundle.
type Decision = Bundle
type Result = Bundle

// ValidationResult is an alias for Acceptance.
type ValidationResult = Acceptance

// Aggregator is a stateless convenience wrapper around Aggregate/Accept.
type Aggregator struct{}

// New returns a stateless aggregator.
func New() *Aggregator { return &Aggregator{} }

// Aggregate selects a valid evidence set.
func (a *Aggregator) Aggregate(req Request) (Bundle, error) { return Aggregate(req) }

// Audit independently re-validates a bundle.
func (a *Aggregator) Audit(req Request, bundle Bundle) Acceptance { return Audit(req, bundle) }

// Accept independently re-validates a bundle.
func (a *Aggregator) Accept(req Request, bundle Bundle) error { return Accept(req, bundle) }

// Select is an alias for Aggregate.
func Select(req Request) (Bundle, error) { return Aggregate(req) }

// Verify is an alias for Accept.
func Verify(req Request, bundle Bundle) error { return Accept(req, bundle) }

// ValidateOrder is the free-function form of Order.Validate.
func ValidateOrder(order Order) error { return order.Validate() }

// NormalizeOrder is the free-function form of Order.Normalize.
func NormalizeOrder(order Order) (Order, error) { return order.Normalize() }

// ValidateWitness is the free-function form of Witness.Validate.
func ValidateWitness(order Order, witness Witness) error { return witness.Validate(order) }
