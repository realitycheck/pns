package main

import (
	"flag"
	"log"
)

var (
	name    string
	version string
	commit  string
	date    string
)

func main() {
	flag.Parse()

	log.Printf("%s: version=%s, commit=%s, date=%s", name, version, commit, date)
	log.Printf("%s: starting...", name)
	defer log.Printf("%s: gg.", name)

}
