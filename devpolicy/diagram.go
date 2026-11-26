package devpolicy

// Diagram is the symbolic two-dimensional interval decision diagram for one
// device type. Its cells form an exact disjoint cover of the whole
// (major, minor) plane and each cell carries the uniform permission mask
// decided for every device inside it. The diagram never stores individual
// device numbers.
type Diagram struct {
	Type DevType
	Partition
}

// newDiagram returns the default-deny single-cell diagram.
func newDiagram(t DevType) *Diagram {
	return &Diagram{Type: t, Partition: fullPartition(0)}
}

// apply folds a single rule into the diagram in rule order.
func (d *Diagram) apply(r Rule) {
	d.Partition = d.Partition.applyRule(r.rect(), r.Effect, r.Perm)
}

// finalize merges adjacent equal-mask cells after all rules are applied.
func (d *Diagram) finalize() {
	d.Partition = compact(d.Partition)
}

// maskAt returns the decided permission mask for one concrete device.
func (d *Diagram) maskAt(major, minor uint16) uint8 {
	for _, cell := range d.Partition {
		if major >= cell.MajMin && major <= cell.MajMax &&
			minor >= cell.MinMin && minor <= cell.MinMax {
			return cell.Mask
		}
	}
	return 0
}

// CompiledPolicy is the immutable output of the hierarchical compiler.
// Effective permission at any device is the per-bit intersection of every
// nested layer's decision, so a child can never resurrect a bit an ancestor
// denied or never granted.
type CompiledPolicy struct {
	// Layers is one ordered diagram per cgroup depth, root first.
	Layers [2]*Diagram
	// Effective intersects all layers symbolically; it is what audits compare.
	Effective [2]*Diagram
	// Rules retains the input rules in compiled order for witness replay.
	Rules []Rule
	// DomainMax is the active upper bound (MaxDeviceNo in production).
	DomainMax uint16
}

// EffectiveMask reports the final rwm mask for one device.
func (c *CompiledPolicy) EffectiveMask(t DevType, major, minor uint16) uint8 {
	if t != TypeChar && t != TypeBlock {
		return 0
	}
	return c.Effective[typeIndex(t)].maskAt(major, minor)
}

// layerCount returns the number of distinct cgroup layers present.
func (c *CompiledPolicy) layerCount() int {
	depth := 0
	for _, r := range c.Rules {
		if r.Layer+1 > depth {
			depth = r.Layer + 1
		}
	}
	return depth
}

// compileLayer builds one device-type diagram for a single layer by replaying
// its rules in (Order, slice position) sequence.
func compileLayer(t DevType, rules []Rule, domainMax uint16) *Diagram {
	d := newDiagram(t)
	for _, r := range rules {
		if r.Type != TypeAll && r.Type != t {
			continue
		}
		if r.MajorMax > domainMax || r.MinorMax > domainMax {
			// Small-domain exhaustive tests clip rules at the domain boundary.
			clipped := r
			clipped.MajorMax = minU16(clipped.MajorMax, domainMax)
			clipped.MinorMax = minU16(clipped.MinorMax, domainMax)
			d.apply(clipped)
			continue
		}
		d.apply(r)
	}
	d.finalize()
	return d
}

// intersectDiagramsDomain is the domain-aware intersection variant.
func intersectDiagramsDomain(a, b *Diagram, domainMax uint16) *Diagram {
	return &Diagram{
		Type:      a.Type,
		Partition: intersectPartitionsDomain(a.Partition, b.Partition, domainMax),
	}
}

// typeIndex maps the ordered device types to array slots. Char sorts before
// block, matching the required counter-example ordering (c < b).
func typeIndex(t DevType) int {
	if t == TypeBlock {
		return 1
	}
	return 0
}

// orderedTypes returns types in counter-example lexicographic order.
func orderedTypes() []DevType {
	return []DevType{TypeChar, TypeBlock}
}
