package hittest

import "testing"

func TestSecureClickConcurrentAccess(t *testing.T) {
	scene := buildFixture()
	click := NewSecureClick(scene)
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			click.Armed()
			click.Snapshot()
			click.Cancel()
		}
	}()

	for i := 0; i < 200; i++ {
		press := click.Press(Point{X: 140, Y: 70})
		if press.Status == ClickArmed {
			_, _ = click.Release(Point{X: 140, Y: 70})
		}
		scene.SetTransform("C", Translate(float64(i), 0))
	}
	<-done
}
