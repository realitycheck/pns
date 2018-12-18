package main

import (
	"flag"
	"fmt"
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
	"github.com/streadway/amqp"
)

var (
	name    string
	version string
	commit  string
	date    string
)

var (
	producerMode       = true
	producerExchange   = "amq.topic"
	producerRoutingKey = "external.event.incoming"
	producerRpsPublish = 100
	producerMaxID      = 100000

	metricsMode = true
	metricsAddr = "0.0.0.0:9901"

	amqpURL = "amqp://guest:guest@external.amqp:5672"

	metricsIncomingCounter = prom.NewCounter(prom.CounterOpts{
		Name: "app_incoming_num_total",
		Help: "The total number of incoming messages",
	})
)

func init() {
	flag.BoolVar(&producerMode, "p", producerMode, "Producer Mode")
	flag.StringVar(&producerExchange, "exchange", producerExchange, "Producer Exchange")
	flag.StringVar(&producerRoutingKey, "routing-key", producerRoutingKey, "Producer Routing Key")
	flag.IntVar(&producerRpsPublish, "rps", producerRpsPublish, "Producer Publish RPS")
	flag.IntVar(&producerMaxID, "max", producerMaxID, "Producer Max ID")

	flag.BoolVar(&metricsMode, "m", metricsMode, "Metrics Mode")
	flag.StringVar(&metricsAddr, "maddr", metricsAddr, "Metrics Addr")

	flag.StringVar(&amqpURL, "amqp", amqpURL, "AMQP URL")

	prom.MustRegister(metricsIncomingCounter)
}

func checkErr(err error) error {
	if err != nil {
		log.Printf("%s: error, reason=%s", name, err)
	}
	return err
}

func dial(f func() interface{}) interface{} {
	b := backoff.Backoff{}
	for {
		res := f()
		if res != nil {
			return res
		}
		time.Sleep(b.Duration())
	}
}

func connectToAMQP(url string) *amqp.Connection {
	return dial(func() interface{} {
		c, err := amqp.Dial(url)
		if checkErr(err) == nil {
			return c
		}
		return nil
	}).(*amqp.Connection)
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

	if producerMode {
		amqpConn := connectToAMQP(amqpURL)
		defer amqpConn.Close()

		p := &producer{
			runtime:    runtime,
			amqpConn:   amqpConn,
			maxID:      producerMaxID,
			rpsPublish: producerRpsPublish,
		}

		runtime.wg.Add(1)
		go func() {
			checkErr(p.publish(producerExchange, producerRoutingKey))
			runtime.wg.Done()
		}()

		log.Printf("%s: producer started", name)
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

type producer struct {
	runtime  *runtime
	amqpConn *amqp.Connection

	maxID int

	rpsPublish int
}

func (p *producer) publish(exchange, routingKey string) error {
	ch, err := p.amqpConn.Channel()
	if err != nil {
		return err
	}

	usersQueue := make(chan int)

	go func() {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		rate := time.Tick(time.Second / time.Duration(p.rpsPublish))

		for {
			select {
			case <-p.runtime.done:
				close(usersQueue)
				return
			case <-rate:
				usersQueue <- int(r.Int31()) % p.maxID
			}

		}
	}()

	for userID := range usersQueue {
		go func(userID string) {
			defer metricsIncomingCounter.Inc()

			err := ch.Publish(
				exchange,
				routingKey,
				false, // mandatory
				false, // immediate
				amqp.Publishing{
					Headers:      amqp.Table{},
					ContentType:  "text/plain",
					Body:         []byte(userID),
					DeliveryMode: amqp.Transient,
					Priority:     0,
				},
			)
			checkErr(err)
		}(fmt.Sprintf("%d", userID))
	}

	return nil
}
