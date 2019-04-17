package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/PingThingsIO/c37splitter"
	yaml "gopkg.in/yaml.v2"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("usage: c37splitter config.yaml\n")
		os.Exit(1)
	}
	conf, err := ioutil.ReadFile(os.Args[1])
	if err != nil {
		fmt.Printf("could not read config file: %v\n", err)
		os.Exit(2)
	}
	params := &c37splitter.SplitterConfig{}
	err = yaml.Unmarshal(conf, &params)
	if err != nil {
		fmt.Printf("could not parse config: %v\n", err)
		os.Exit(3)
	}
	c37splitter.StartSplitter(params)
	for {
		time.Sleep(5 * time.Second)
	}
}
