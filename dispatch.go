package main

import (
	"github.com/garyburd/redigo/redis"
)

// Dispatch manages the workers.
type Dispatch struct {
	Queue  chan<- Impression
	Worker *Worker
}

// NewDispatch creates a new worker dispatcher.
func NewDispatch(p *redis.Pool) *Dispatch {
	var queue = make(chan Impression, PublishBuffer)

	return &Dispatch{
		Queue:  queue,
		Worker: NewWorker(p, queue),
	}
}

// Start sets up and begins all the workers.
func (d *Dispatch) Start() {

	// start up the publisher worker
	go d.Worker.Run()
}

// Close shuts down the workers on the dispatcher.
func (d *Dispatch) Close() {
	d.Worker.quit <- struct{}{}

	close(d.Queue)

	<-d.Worker.shutdown
}
