package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jpillora/backoff"
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
	producerMaxID      = 1000000

	amqpURL = "amqp://guest:guest@external.amqp:5672"
)

func init() {
	flag.BoolVar(&producerMode, "p", producerMode, "Producer Mode")
	flag.StringVar(&producerExchange, "exchange", producerExchange, "Producer Exchange")
	flag.StringVar(&producerRoutingKey, "routing-key", producerRoutingKey, "Producer Routing Key")
	flag.IntVar(&producerRpsPublish, "rps", producerRpsPublish, "Producer Publish RPS")
	flag.IntVar(&producerMaxID, "max", producerMaxID, "Producer Max ID")

	flag.StringVar(&amqpURL, "amqp", amqpURL, "AMQP URL")
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

	amqpConn := connectToAMQP(amqpURL)
	defer amqpConn.Close()

	p := &producer{runtime, amqpConn, make(chan int), producerMaxID, producerRpsPublish}
	runtime.wg.Add(1)

	go func() {
		checkErr(p.publish(producerExchange, producerRoutingKey))
		runtime.wg.Done()
	}()

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

	users chan int
	maxID int

	rpsPublish int
}

func (p *producer) publish(exchange, routingKey string) error {
	ch, err := p.amqpConn.Channel()
	if err != nil {
		return err
	}

	go func() {
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		rate := time.Tick(time.Second / time.Duration(p.rpsPublish))

		for {
			select {
			case <-p.runtime.done:
				close(p.users)
				return
			default:
				<-rate
			}
			p.users <- int(r.Int31()) % p.maxID
		}
	}()

	for userID := range p.users {
		go func(userID string) {
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
