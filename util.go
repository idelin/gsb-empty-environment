package main

import "io/ioutil"

func writeFile(path, content string) error {
	return ioutil.WriteFile(path, []byte(content), 0644)
}
