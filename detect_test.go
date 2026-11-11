package sqlifinger

import "testing"

// 绕过样本：覆盖注释替换空白、版本注释、嵌套括号、大小写拼接、
// 十六进制字面量、反引号、|| 拼接、双引号上下文、堆叠查询等手法。
var bypassSamples = []string{
	`1' OR '1'='1`,
	`1' OR '1'='1'-- -`,
	`1'/**/OR/**/'1'='1`,
	`1' oR '1'='1`,
	`1' OR 'x'='x`,
	`admin' OR '1'='1'#`,
	`1'||'1'='1`,
	`1'||1=1#`,
	`1' XOR 1=1-- -`,
	`1' OR 2>1-- -`,
	`1' OR 1=1 LIMIT 1-- -`,
	`1' AND 'a'='a`,
	`1' AND 0x1=0x1-- -`,
	`1' AND 0x4141=0x4141-- -`,
	"1' OR `1`=`1`-- -",
	`1'/*!AND*/'1'='1`,
	`1'/*!50000AND*/1=1-- -`,
	`1' AND((1)=(1))-- -`,
	`1' AND (SELECT 1 FROM (SELECT SLEEP(5))a)-- -`,
	`1' AND (SELECT count(*) FROM information_schema.tables)>0-- -`,
	`1' AND SLEEP(5)-- -`,
	`1' AND BENCHMARK(5000000,MD5(1))-- -`,
	`1' UNioN SeLEct 1,2,3-- -`,
	`-1' UNION ALL SELECT NULL,NULL,version()-- -`,
	`1' UNION(SELECT 1,2)-- -`,
	`1'; DROP TABLE users-- -`,
	`'; DELETE FROM users WHERE '1'='1`,
	`') OR ('1'='1`,
	`1 OR 1=1`,
	`1 OR 1=1-- -`,
	`1" OR "1"="1`,
	`1" AND "1"="1"-- -`,
	`1" UNION SELECT 1,2-- -`,
}

// 正常业务参数：姓名、邮箱、日期、表达式、URL、JSON、自然语言等。
var normalSamples = []string{
	`O'Brien`,
	`it's a test`,
	`rock 'n' roll`,
	`Tom & Jerry`,
	`Tom & Jerry's`,
	`B&B`,
	`50% off`,
	`100%`,
	`C++`,
	`user@example.com`,
	`2024-01-01`,
	`1+1=2`,
	`5 >= 3`,
	`price >= 100`,
	`key=value`,
	`hello; world`,
	`x--y`,
	`1-- comment`,
	`abc#def`,
	`mother-in-law`,
	`select`,
	`union`,
	`Grand Union Hotel`,
	`and 1 more thing`,
	`size 10 and 1`,
	`(required)`,
	`3.14`,
	`0x1F`,
	`中文参数名`,
	`it's 5 o'clock`,
	`We are 100% sure; guaranteed!`,
	`https://example.com/a?b=c&d=1`,
	`{"key": "value", "n": 1}`,
	`AND (see details)`,
	`and sleep well`,
	`100`,
	`hello world`,
}

func TestDetectBypassSamples(t *testing.T) {
	d := DefaultDetector()
	for _, s := range bypassSamples {
		det, ok := d.Detect(s)
		if !ok {
			t.Errorf("missed bypass sample %q", s)
			continue
		}
		t.Logf("detected %q ctx=%s pattern=%q fp=%q", s, det.Context, det.Pattern, det.Fingerprint)
	}
}

func TestNormalSamplesNoFalsePositives(t *testing.T) {
	d := DefaultDetector()
	for _, s := range normalSamples {
		if det, ok := d.Detect(s); ok {
			t.Errorf("false positive on %q: ctx=%s pattern=%q fp=%q", s, det.Context, det.Pattern, det.Fingerprint)
		}
	}
}

func TestDetectionAndFalsePositiveRates(t *testing.T) {
	d := DefaultDetector()

	detected := 0
	for _, s := range bypassSamples {
		if _, ok := d.Detect(s); ok {
			detected++
		}
	}
	fp := 0
	for _, s := range normalSamples {
		if _, ok := d.Detect(s); ok {
			fp++
		}
	}

	detRate := float64(detected) / float64(len(bypassSamples))
	fpRate := float64(fp) / float64(len(normalSamples))
	t.Logf("detection rate: %d/%d = %.1f%%", detected, len(bypassSamples), detRate*100)
	t.Logf("false positive rate: %d/%d = %.1f%%", fp, len(normalSamples), fpRate*100)

	if detRate < 0.95 {
		t.Errorf("detection rate %.1f%% below 95%%", detRate*100)
	}
	if fpRate > 0.0 {
		t.Errorf("false positive rate %.1f%% above 0%%", fpRate*100)
	}
}

func TestConfigurableFingerprints(t *testing.T) {
	d := NewDetector("kv")
	if _, ok := d.Detect(`1 OR 1=1`); ok {
		t.Error("custom detector should not use default fingerprints")
	}
	d.Add("&1o1")
	if _, ok := d.Detect(`1 OR 1=1`); !ok {
		t.Error("added fingerprint not honored")
	}

	d2 := DefaultDetector()
	d2.Add("vvvv")
	if _, ok := d2.Detect(`a b c d`); !ok {
		t.Error("custom fingerprint on default detector not honored")
	}
}
