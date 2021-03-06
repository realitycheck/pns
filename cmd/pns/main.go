package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/gomodule/redigo/redis"
	"github.com/jpillora/backoff"
	prom "github.com/prometheus/client_golang/prometheus"
	"github.com/streadway/amqp"
)

var (
	name    string
	version string
	commit  string
	date    string
)

var (
	serverMode        = false
	serverHost        = "app.server"
	serverPort        = 9019
	serverRpsIncoming = 0
	serverRpsUnread   = 0
	serverRpsRead     = 0
	serverTimeUnread  time.Duration
	serverTimeRead    time.Duration

	workerMode       = false
	workerExchange   = "amq.topic"
	workerRoutingKey = "external.event.incoming"
	workerQueue      = "app.queue"

	metricsMode = true
	metricsAddr = "0.0.0.0:9901"

	redisURL = "redis://app.redis:6379"
	httpURL  = "http://external.http:8080"
	amqpURL  = "amqp://guest:guest@external.amqp:5672"

	metricsDurationSummary = prom.NewSummaryVec(prom.SummaryOpts{
		Name: "app_duration_seconds",
		Help: "A summary of handler's duration in seconds",
	}, []string{
		"handler",
	})
)

func init() {
	flag.BoolVar(&serverMode, "s", serverMode, "Server Mode")
	flag.StringVar(&serverHost, "host", serverHost, "Server Host")
	flag.IntVar(&serverPort, "port", serverPort, "Server Port")
	flag.IntVar(&serverRpsIncoming, "rps-incoming", serverRpsIncoming, "Server Incoming RPS")
	flag.IntVar(&serverRpsUnread, "rps-unread", serverRpsUnread, "Server Unread RPS")
	flag.IntVar(&serverRpsRead, "rps-read", serverRpsRead, "Server Read RPS")
	flag.DurationVar(&serverTimeUnread, "time-unread", serverTimeUnread, "Server Unread Busy Time")
	flag.DurationVar(&serverTimeRead, "time-read", serverTimeRead, "Server Read Busy Time")

	flag.BoolVar(&workerMode, "w", workerMode, "Worker Mode")
	flag.StringVar(&workerExchange, "exchange", workerExchange, "Worker Exchange")
	flag.StringVar(&workerRoutingKey, "routing-key", workerRoutingKey, "Worker Routing Key")
	flag.StringVar(&workerQueue, "queue", workerQueue, "Worker Queue")

	flag.StringVar(&redisURL, "redis", redisURL, "Redis URL")
	flag.StringVar(&httpURL, "http", httpURL, "HTTP URL")
	flag.StringVar(&amqpURL, "amqp", amqpURL, "AMQP URL")

	flag.BoolVar(&metricsMode, "m", metricsMode, "Metrics Mode")
	flag.StringVar(&metricsAddr, "maddr", metricsAddr, "Metrics Addr")

	prom.MustRegister(metricsDurationSummary)
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

func connectToRedis(url string) redis.Conn {
	return dial(func() interface{} {
		c, err := redis.DialURL(url, redis.DialConnectTimeout(5*time.Second))
		if checkErr(err) == nil {
			return c
		}
		return nil
	}).(redis.Conn)
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

func newRedisPool(url string) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     50,
		IdleTimeout: 100 * time.Second,
		Dial: func() (redis.Conn, error) {
			return connectToRedis(url), nil
		},
	}
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

	if workerMode {
		redisPool := newRedisPool(redisURL)
		defer redisPool.Close()

		amqpConn := connectToAMQP(amqpURL)
		defer amqpConn.Close()

		w := worker{
			runtime:   runtime,
			redisPool: redisPool,
			amqpConn:  amqpConn,
		}

		runtime.wg.Add(1)
		go func() {
			checkErr(w.listen(workerExchange, workerRoutingKey, workerQueue))
			runtime.wg.Done()
		}()

		log.Printf("%s: worker started", name)
	}

	if serverMode {
		redisPool := newRedisPool(redisURL)
		defer redisPool.Close()

		httpClient := &http.Client{}

		s := server{
			runtime:     runtime,
			redisPool:   redisPool,
			httpClient:  httpClient,
			httpURL:     httpURL,
			rpsIncoming: serverRpsIncoming,
			rpsUnread:   serverRpsUnread,
			rpsRead:     serverRpsRead,
			timeUnread:  serverTimeUnread,
			timeRead:    serverTimeRead,
		}

		runtime.wg.Add(1)
		go func() {
			checkErr(s.listen(serverHost, serverPort))
			runtime.wg.Done()
		}()

		log.Printf("%s: server started", name)
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

type worker struct {
	runtime *runtime

	redisPool *redis.Pool
	amqpConn  *amqp.Connection
}

func (w *worker) listen(exchange, routingKey, queue string) error {
	ch, err := w.amqpConn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		queue,
		true,  // durable
		false, // autoDelete
		false, // exclusive
		false, // noWait
		nil,   // args
	)

	if err != nil {
		return nil
	}

	err = ch.QueueBind(
		q.Name,
		routingKey,
		exchange,
		false, // noWait
		nil,   // args
	)
	if err != nil {
		return nil
	}

	dd, err := ch.Consume(
		q.Name,
		"",    // consumer
		false, //autoAck
		false, //exclusive
		false, // noLocal
		false, // noWait
		nil,
	)

	go func() {
		for d := range dd {
			w.handleDelivery(&d)
		}
	}()

	<-w.runtime.done

	return nil
}

func (w *worker) handleDelivery(d *amqp.Delivery) {
	t := time.Now()
	defer metricsDurationSummary.WithLabelValues("handleDelivery").Observe(time.Since(t).Seconds())

	redisConn := w.redisPool.Get()
	defer redisConn.Close()

	id, err := strconv.ParseInt(string(d.Body), 10, 64)
	if checkErr(err) == nil {
		incomingKey := fmt.Sprintf("incoming:%d", id)
		_, err = redisConn.Do("SET", incomingKey, 1)
		checkErr(err)
	}
	d.Ack(false)
}

type server struct {
	runtime *runtime

	redisPool  *redis.Pool
	httpClient *http.Client
	httpURL    string

	rpsIncoming int
	rpsUnread   int
	rpsRead     int

	timeUnread time.Duration
	timeRead   time.Duration
}

func rpsRestricted(rps int) func(http.HandlerFunc) http.HandlerFunc {
	return func(h http.HandlerFunc) http.HandlerFunc {
		if rps == 0 {
			return h
		}
		rate := time.Tick(time.Second / time.Duration(rps))
		return func(w http.ResponseWriter, r *http.Request) {
			<-rate
			h(w, r)
		}
	}
}

func promSummary(handler string) func(http.HandlerFunc) http.HandlerFunc {
	return func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			t := time.Now()
			defer func(t time.Time) {
				metricsDurationSummary.WithLabelValues(handler).Observe(time.Since(t).Seconds())
			}(t)
			h(w, r)
		}
	}
}

func (s *server) listen(host string, port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/incoming", rpsRestricted(s.rpsIncoming)(promSummary("handleIncoming")(s.handleIncoming)))
	mux.HandleFunc("/unread", rpsRestricted(s.rpsUnread)(promSummary("handleUnread")(s.handleUnread)))
	mux.HandleFunc("/read", rpsRestricted(s.rpsRead)(promSummary("handleRead")(s.handleRead)))

	addr := fmt.Sprintf("%s:%d", host, port)

	h := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-s.runtime.done

		checkErr(h.Shutdown(context.Background()))
	}()

	return h.ListenAndServe()
}

func (s *server) handleIncoming(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	paramID := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(paramID, 10, 64)
	if err := checkErr(err); err != nil {
		w.WriteHeader(400)
		return
	}

	redisConn := s.redisPool.Get()
	defer redisConn.Close()

	incomingKey := fmt.Sprintf("incoming:%d", id)
	incoming, _ := redis.Int(redisConn.Do("GET", incomingKey))
	fmt.Fprintf(w, "{\"%d\": %d}", id, incoming)
}

func (s *server) handleUnread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	paramID := r.URL.Query().Get("id")
	_, err := strconv.ParseInt(paramID, 10, 64)
	if err := checkErr(err); err != nil {
		w.WriteHeader(400)
		return
	}

	resp, err := s.httpClient.Get(s.httpURL + "/api/unread")
	if err := checkErr(err); err != nil {
		w.WriteHeader(400)
		return
	}

	defer resp.Body.Close()
	io.Copy(w, resp.Body)

	if s.timeUnread > 0 {
		time.Sleep((&backoff.Backoff{
			Min:    s.timeUnread,
			Max:    s.timeUnread,
			Factor: 1,
			Jitter: true,
		}).Duration())
	}
}

func (s *server) handleRead(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	paramID := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(paramID, 10, 64)
	if err := checkErr(err); err != nil {
		w.WriteHeader(400)
		return
	}

	resp, err := s.httpClient.Post(s.httpURL+"/api/read", "application/json", nil)
	if err := checkErr(err); err != nil {
		w.WriteHeader(400)
		return
	}

	defer resp.Body.Close()
	io.Copy(w, resp.Body)

	if s.timeRead > 0 {
		time.Sleep((&backoff.Backoff{
			Min:    s.timeRead,
			Max:    s.timeRead,
			Factor: 1,
			Jitter: true,
		}).Duration())
	}

	redisConn := s.redisPool.Get()
	defer redisConn.Close()

	incomingKey := fmt.Sprintf("incoming:%d", id)
	_, err = redisConn.Do("DEL", incomingKey)
	checkErr(err)
}
