package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/garyburd/redigo/redis"
)

// Worker writes out the impression data to the redis queue.
type Worker struct {
	Queue    <-chan Impression
	Interval time.Duration
	Size     int
	Pool     *redis.Pool

	quit     chan struct{}
	shutdown chan struct{}
	Verbose  bool
}

// NewWorker creates a new worker instance.
func NewWorker(r *redis.Pool, queue <-chan Impression) *Worker {
	return &Worker{
		Pool:     r,
		Size:     PublishWorkerBuffer,
		Interval: PublishWorkerTimeout,
		Queue:    queue,
		quit:     make(chan struct{}),
		shutdown: make(chan struct{}),
		Verbose:  os.Getenv("VERBOSE") == "true",
	}
}

func (w *Worker) verbose(msg string, args ...interface{}) {
	if w.Verbose {
		log.Printf(fmt.Sprintf("impression_queue: %s", msg), args...)
	}
}

// Run processes Impression data.
func (w *Worker) Run() {
	conn := w.Pool.Get()
	defer conn.Close()

	var msgs = make([]Impression, 0, w.Size)
	tick := time.NewTicker(w.Interval)

	w.verbose("active")

	for {
		select {
		case msg := <-w.Queue:
			w.verbose("buffer (%d/%d) %v", len(msgs), w.Size, msg)
			msgs = append(msgs, msg)
			if len(msgs) >= w.Size {
				w.verbose("exceeded %d messages – flushing", w.Size)
				w.send(conn, msgs)
				msgs = make([]Impression, 0, w.Size)
			}
		case <-tick.C:
			if len(msgs) > 0 {
				w.verbose("interval reached - flushing %d", len(msgs))
				w.send(conn, msgs)
				msgs = make([]Impression, 0, w.Size)
			} else {
				w.verbose("interval reached – nothing to send")
			}
		case <-w.quit:
			tick.Stop()
			w.verbose("exit requested – draining msgs")
			// drain the msg channel.
			for msg := range w.Queue {
				w.verbose("buffer (%d/%d) %v", len(msgs), w.Size, msg)
				msgs = append(msgs, msg)
			}
			w.verbose("exit requested – flushing %d", len(msgs))
			w.send(conn, msgs)
			w.verbose("exit")
			w.shutdown <- struct{}{}
			return
		}
	}
}

func (w *Worker) send(con redis.Conn, msgs []Impression) {
	w.verbose("processing %d msgs\n", len(msgs))

	var args = []interface{}{"impressions"}

	for _, i := range msgs {
		impressionData, err := json.Marshal(i)
		if err != nil {
			w.verbose("error %s: %s\n", err.Error())
			return
		}

		args = append(args, string(impressionData))
	}

	err := con.Send("LPUSH", args...)
	if err != nil {
		w.verbose("error: %s\n", err.Error())
		return
	}

	err = con.Flush()
	if err != nil {
		w.verbose("error: %s\n", err.Error())
		return
	}

	w.verbose("processed\n")
}

// Close shuts down and flushes the workers.
func (w *Worker) Close() {
	w.quit <- struct{}{}
	<-w.shutdown
	return
}
