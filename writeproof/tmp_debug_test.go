package writeproof

import "testing"

func TestTmpDebug(t *testing.T) {
	write := WriteSegment{Start:103, End:251, Mask:133, Match:4, Transform: AffineTransform{
		Rows:[48]uint64{23,251,253,45,113,187,97,244}, Bias:21,
	}}
	protection := Protection{Kind:ProtectionLogical, Start:12, End:247, Transform: AffineTransform{
		Rows:[48]uint64{78,55,241,177,138,38,165,83}, Bias:248,
	}}
	req := Request{Writes:[]WriteSegment{write}, Protections:[]Protection{protection}, EraseBlockSize:256, PhysicalBits:8, Budget:Budget{Unlimited:true}, logicalBits:8}
	report, err := Prove(req)
	if err != nil { t.Fatal(err) }
	t.Logf("report=%+v conflict=%+v", report.Status, report.Conflict)
	domain, _ := decomposeMaskedDomain(103,251,133,4,8,nil)
	t.Logf("x108 selected=%v domain=%+v", 108&133 == 4, domain.Cubes)
	for _, cube := range domain.Cubes {
		if 108&^cube.FreeMask == cube.Base&^cube.FreeMask {
			t.Logf("covered by %+v", cube)
		}
	}
}
