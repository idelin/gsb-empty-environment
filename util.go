package main

import "io/ioutil"

func writeFile(path string, data []byte) error {
	return ioutil.WriteFile(path, data, 0644)
}
