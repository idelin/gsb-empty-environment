package pktscale

import (
	"bytes"
	"reflect"
	"testing"
)

// 输入分片 + 暂停（存档/恢复）必须与一次性处理结果完全一致。
func TestCheckpointResumeEquivalence(t *testing.T) {
	mkRecs := func() []Record {
		var recs []Record
		for i := 0; i < 500; i++ {
			recs = append(recs, Record{
				Seq:    int64(i),
				Stream: i % 4,
				Time:   int64(i/4) * 3,
				Size:   int64(37 + (i*13)%211),
			})
		}
		return recs
	}

	// 一次性基线（含一次倍率变化）。
	oneShot := func() []Fragment {
		s, err := NewScaler(rat(3, 7), rat(5, 3), 100, 1)
		if err != nil {
			t.Fatal(err)
		}
		var out []Fragment
		for i, rec := range mkRecs() {
			if i == 250 {
				if err := s.SetTimeRate(rat(7, 3), rec.Time); err != nil {
					t.Fatal(err)
				}
			}
			f, err := s.Process(rec)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, f...)
		}
		return out
	}()

	// 分块输入，每块之间保存/恢复（模拟暂停与继续），块大小故意不规则。
	chunked := func() []Fragment {
		s, err := NewScaler(rat(3, 7), rat(5, 3), 100, 1)
		if err != nil {
			t.Fatal(err)
		}
		var out []Fragment
		recs := mkRecs()
		i := 0
		for _, chunk := range []int{1, 7, 100, 13, 250, 129} {
			// 暂停：保存并重新加载。
			var buf bytes.Buffer
			if err := s.Save(&buf); err != nil {
				t.Fatal(err)
			}
			s, err = Load(&buf)
			if err != nil {
				t.Fatal(err)
			}
			for ; chunk > 0 && i < len(recs); chunk-- {
				if i == 250 {
					if err := s.SetTimeRate(rat(7, 3), recs[i].Time); err != nil {
						t.Fatal(err)
					}
				}
				f, err := s.Process(recs[i])
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, f...)
				i++
			}
		}
		if i != len(recs) {
			t.Fatalf("consumed %d of %d records", i, len(recs))
		}
		return out
	}()

	if !reflect.DeepEqual(oneShot, chunked) {
		for i := range oneShot {
			if oneShot[i] != chunked[i] {
				t.Fatalf("mismatch at fragment %d:\n one-shot %+v\n chunked  %+v", i, oneShot[i], chunked[i])
			}
		}
		t.Fatal("outputs differ")
	}
}

// 存档内容必须是有理数精确状态（可恢复、无精度损失）。
func TestCheckpointIsRationalAndExact(t *testing.T) {
	s, _ := NewScaler(rat(1, 3), rat(1, 7), 100, 1)
	for i := 0; i < 10; i++ {
		if _, err := s.Process(Record{Seq: int64(i), Stream: 0, Time: int64(i), Size: 3}); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := s.Save(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"1/3"`)) {
		t.Fatalf("checkpoint does not store exact rational rate:\n%s", buf.String())
	}
	s2, err := Load(&buf)
	if err != nil {
		t.Fatal(err)
	}
	// 恢复后继续处理，与不间断处理逐片一致。
	var a, b []Fragment
	for i := 10; i < 30; i++ {
		fa, _ := s.Process(Record{Seq: int64(i), Stream: 0, Time: int64(i), Size: 3})
		fb, _ := s2.Process(Record{Seq: int64(i), Stream: 0, Time: int64(i), Size: 3})
		a = append(a, fa...)
		b = append(b, fb...)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("resumed scaler diverged from continuous run")
	}
}
