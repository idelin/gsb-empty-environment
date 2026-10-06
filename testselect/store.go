package testselect

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

// Save 将快照以 JSON 形式写入文件。
func Save(snap *Snapshot, path string) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}

// Load 从文件读取快照；文件不存在时返回 nil 快照与 nil 错误，
// 由 Select 按“无基线”处理为全量运行。
func Load(path string) (*Snapshot, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}
