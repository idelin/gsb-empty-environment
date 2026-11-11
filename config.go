package repartition

func configEqual(a, c Config) bool {
	a = a.normalized()
	c = c.normalized()
	if len(a.OldPartitions) != len(c.OldPartitions) || len(a.NewPartitions) != len(c.NewPartitions) {
		return false
	}
	for i := range a.OldPartitions {
		if a.OldPartitions[i] != c.OldPartitions[i] {
			return false
		}
	}
	for i := range a.NewPartitions {
		if a.NewPartitions[i] != c.NewPartitions[i] {
			return false
		}
	}
	if len(a.CutOffsets) != len(c.CutOffsets) {
		return false
	}
	for k, v := range a.CutOffsets {
		if c.CutOffsets[k] != v {
			return false
		}
	}
	return true
}
