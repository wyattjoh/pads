package main

import (
	"time"

	"github.com/satori/go.uuid"
)

// NewImpression returns a new Impression.
func NewImpression(adid string) Impression {
	return Impression{
		ID:        uuid.NewV4().String(),
		AdID:      adid,
		Timestamp: time.Now(),
	}
}

// Impression is a record of impressions on the given advertisement.
type Impression struct {
	ID        string
	AdID      string
	Timestamp time.Time
}
