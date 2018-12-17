package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"time"
)

var (
	name    string
	version string
	commit  string
	date    string
)

var (
	httpURL = "http://app.server:9019"

	consumerMode       = true
	consumerRpsConsume = 5000
	consumerMaxID      = 1000000
)

func init() {
	flag.BoolVar(&consumerMode, "c", consumerMode, "Consumer Mode")
	flag.IntVar(&consumerRpsConsume, "rps", consumerRpsConsume, "Consumer Consume RPS")
	flag.IntVar(&consumerMaxID, "max", consumerMaxID, "Consumer Max ID")

	flag.StringVar(&httpURL, "http", httpURL, "HTTP URL")
}

func checkErr(err error) error {
	if err != nil {
		log.Printf("%s: error, reason=%s", name, err)
	}
	return err
}

func main() {
	flag.Parse()

	log.Printf("%s: version=%s, commit=%s, date=%s", name, version, commit, date)
	log.Printf("%s: starting...", name)
	defer log.Printf("%s: gg.", name)

	httpClient := &http.Client{}

	c := &consumer{httpClient, httpURL}

	users := make(chan int)

	go func() {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		rate := time.Tick(time.Second / time.Duration(consumerRpsConsume))
		for {
			select {
			default:
				<-rate
			}
			users <- int(r.Int31()) % consumerMaxID
		}
	}()

	for id := range users {
		checkErr(c.consume(id))
	}
}

type consumer struct {
	httpClient *http.Client
	httpURL    string
}

func (c *consumer) url(path string, id int) string {
	return fmt.Sprintf("%s/%s?id=%d", c.httpURL, path, id)
}

func (c *consumer) consume(id int) error {
	resp, err := c.httpClient.Get(c.url("incoming", id))
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	res := make(map[int]int)
	err = json.NewDecoder(resp.Body).Decode(&res)
	log.Printf("%s: user=%d, incoming=%d", name, id, res[id])

	if res[id] == 1 {

	}

	return nil
}
