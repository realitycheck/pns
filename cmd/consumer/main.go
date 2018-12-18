package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jpillora/backoff"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	name    string
	version string
	commit  string
	date    string
)

var (
	consumerMode       = true
	consumerRpsConsume = 5000
	consumerMaxID      = 100000
	consumerCount      = 5
	consumerTimeRead   time.Duration

	metricsMode = true
	metricsAddr = "0.0.0.0:9901"

	httpURL = "http://app.server:9019"

	metricsPollingCounter = prom.NewCounter(prom.CounterOpts{
		Name: "app_polling_num_total",
		Help: "The number of consuming polls",
	})
	metricsReadingCounter = prom.NewCounter(prom.CounterOpts{
		Name: "app_reading_num_total",
		Help: "The number of consuming reads",
	})
)

func init() {
	flag.BoolVar(&consumerMode, "c", consumerMode, "Consumer Mode")
	flag.IntVar(&consumerRpsConsume, "rps", consumerRpsConsume, "Consumer Consume RPS")
	flag.IntVar(&consumerMaxID, "max", consumerMaxID, "Consumer Max ID")
	flag.IntVar(&consumerCount, "cnt", consumerCount, "Consumer Count")
	flag.DurationVar(&consumerTimeRead, "time-read", consumerTimeRead, "Consumer Read Busy Time")

	flag.BoolVar(&metricsMode, "m", metricsMode, "Metrics Mode")
	flag.StringVar(&metricsAddr, "maddr", metricsAddr, "Metrics Addr")

	flag.StringVar(&httpURL, "http", httpURL, "HTTP URL")

	prom.MustRegister(metricsPollingCounter)
	prom.MustRegister(metricsReadingCounter)
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

	runtime := &runtime{
		wg:   &sync.WaitGroup{},
		done: make(chan struct{}),
	}

	if consumerMode {
		httpClient := &http.Client{}

		c := &consumer{
			runtime:    runtime,
			httpClient: httpClient,
			httpURL:    httpURL,
			maxID:      consumerMaxID,
			rpsConsume: consumerRpsConsume,
			timeRead:   consumerTimeRead,
		}

		go func() {
			checkErr(c.consume(consumerCount))
			runtime.wg.Done()
		}()

		log.Printf("%s: consumer started", name)
	}

	if metricsMode {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		go checkErr(http.ListenAndServe(metricsAddr, mux))

		log.Printf("%s: monitoring started", name)
	}

	log.Printf("%s: running...", name)
	exit := make(chan os.Signal, 1)
	signal.Notify(exit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-exit:
		close(runtime.done)
	}

	log.Printf("%s: stopping...", name)
	runtime.wg.Wait()
}

type runtime struct {
	wg   *sync.WaitGroup
	done chan struct{}
}

type consumer struct {
	runtime *runtime

	httpClient *http.Client
	httpURL    string

	maxID int

	rpsConsume int

	timeRead time.Duration
}

func (c *consumer) url(path string, id int) string {
	return fmt.Sprintf("%s/%s?id=%d", c.httpURL, path, id)
}

func (c *consumer) consume(num int) error {
	usersQueue := make(chan int, consumerRpsConsume)

	go func() {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		rate := time.Tick(time.Second / time.Duration(c.rpsConsume))

		for {
			select {
			case <-c.runtime.done:
				close(usersQueue)
				return
			case <-rate:
				usersQueue <- int(r.Int31()) % consumerMaxID
			}
		}
	}()

	wg := sync.WaitGroup{}
	wg.Add(num)
	for i := 0; i < num; i++ {
		go func() {
			for id := range usersQueue {
				go func(id int) {
					checkErr(c.doPoll(id))
				}(id)
			}
			wg.Done()
		}()
	}
	wg.Wait()

	return nil
}

func (c *consumer) doPoll(id int) error {
	defer metricsPollingCounter.Inc()

	resp, err := c.httpClient.Get(c.url("incoming", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	res := make(map[int]int)
	err = json.NewDecoder(resp.Body).Decode(&res)

	if res[id] == 1 {
		c.doRead(id)
	}

	return nil
}

func (c *consumer) doRead(id int) error {
	defer metricsReadingCounter.Inc()

	resp, err := c.httpClient.Get(c.url("unread", id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	if c.timeRead > 0 {
		time.Sleep((&backoff.Backoff{
			Min:    c.timeRead,
			Max:    c.timeRead,
			Factor: 1,
			Jitter: true,
		}).Duration())
	}

	resp, err = c.httpClient.Post(c.url("read", id), "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return nil
}
