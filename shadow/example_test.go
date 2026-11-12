package shadow_test

import (
	"fmt"
	"math"

	"example.org/domainreissue/q038/shadow"
)

func ExamplePlan() {
	proj := shadow.Perspective(math.Pi/4, 16.0/9.0, 0.1, 300.0)
	view := shadow.LookAt(shadow.Vec3{Z: 10}, shadow.Vec3{Z: -1}, shadow.Vec3{Y: 1})

	res, err := shadow.Plan(shadow.Config{
		Projection:     proj,
		View:           view,
		LightDirection: shadow.Vec3{X: -0.4, Y: -1, Z: -0.25},
		Splits:         []float64{0.1, 0.3, 1.0},
		ShadowMapSize:  2048,
		ReverseZ:       false,
		CasterRange:    &shadow.CasterRange{Min: -40, Max: 60},
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	for _, c := range res.Cascades {
		// Upload c.LightViewProjection to the shadow-map constant buffer.
		fmt.Printf("cascade %d radius=%.3f texel=%.5f\n", c.Index, c.Radius, c.TexelWorld)
	}
	// Output:
	// cascade 0 radius=0.094 texel=0.00009
	// cascade 1 radius=0.122 texel=0.00012
	// cascade 2 radius=294.488 texel=0.28759
}
