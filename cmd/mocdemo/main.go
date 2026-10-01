// mocdemo 演示多对象容器:追加、按 ID/版本读取、原子发布、
// 索引损坏后的扫描重建(并标明来源)。
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"moc"
)

func main() {
	dir, err := ioutil.TempDir("", "moc-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "objects.moc")

	c, err := moc.Create(path)
	must(err)

	// 1. 追加对象;重名 ID 自动递增版本。
	v1, _ := c.Append("report", []byte("Q1 revenue: 100"))
	c.Append("config", mustMarshal(map[string]interface{}{
		"host": "example.com",
		"port": int64(8080),
		"tls":  true,
	}))
	v2, _ := c.Append("report", []byte("Q1 revenue: 120 (revised)"))
	fmt.Printf("追加完成: report v%d -> v%d, config v1\n", v1, v2)
	fmt.Printf("IDs: %v, report 版本: %v\n", c.IDs(), c.Versions("report"))

	// 2. 按 ID 读取(默认最新),按版本读取历史。
	latest, _ := c.Get("report")
	old, _ := c.GetVersion("report", 1)
	fmt.Printf("report 最新: %s\n", latest)
	fmt.Printf("report v1  : %s\n", old)

	// 3. 重新打开:使用尾部已发布索引。
	c2, err := moc.Open(path)
	must(err)
	fmt.Printf("重开容器, 索引来源: %s\n", c2.Source())

	// 4. 模拟索引损坏(覆盖尾部 magic),再打开 -> 扫描重建。
	f, _ := os.OpenFile(path, os.O_RDWR, 0)
	info, _ := f.Stat()
	f.WriteAt([]byte("CORRUPT!"), info.Size()-8)
	f.Close()

	c3, err := moc.Open(path)
	must(err)
	fmt.Printf("索引损坏后重开, 索引来源: %s\n", c3.Source())
	got, _ := c3.Get("report")
	fmt.Printf("重建后 report 最新: %s\n", got)

	// 5. 模拟崩溃留下的未完成尾部:不进入已发布索引。
	f, _ = os.OpenFile(path, os.O_RDWR, 0)
	info, _ = f.Stat()
	f.WriteAt([]byte("MOCR"), info.Size()) // 半条记录
	f.Close()
	c4, err := moc.Open(path)
	must(err)
	fmt.Printf("存在未完成尾部, IDs 仍为: %v (来源 %s)\n", c4.IDs(), c4.Source())
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustMarshal(v interface{}) []byte {
	b, err := moc.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
