package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/garyburd/redigo/redis"
	_ "github.com/lib/pq"
	"github.com/satori/go.uuid"
)

// ErrNotFound is returned when the advertisement requested is not found.
var ErrNotFound = errors.New("No Advertisement Found")

// SetupDBStmt contains the setup sql for the application.
const SetupDBStmt = `
CREATE TABLE IF NOT EXISTS ad(
  adid      TEXT PRIMARY KEY,
  adweight  INTEGER,
  adimg     TEXT,
  adimgalt  TEXT,
  adimgw    INTEGER,
  adimgh    INTEGER,
  adimglink TEXT,
  adtext    TEXT
);

CREATE TABLE IF NOT EXISTS impression(
	id				TEXT PRIMARY KEY,
	adid      TEXT,
	timestamp TIMESTAMP WITH TIME ZONE,

	FOREIGN KEY(adid) REFERENCES ad(adid)
);
`

// newDB creates a new database handle.
func newDB(path string) *sql.DB {
	db, err := sql.Open("postgres", path)
	if err != nil {
		panic(err)
	}

	_, err = db.Exec(SetupDBStmt)
	if err != nil {
		panic(err)
	}

	return db
}

func newPool(server, password string) *redis.Pool {
	return &redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", server)
			if err != nil {
				return nil, err
			}
			if password != "" {
				if _, err = c.Do("AUTH", password); err != nil {
					c.Close()
					return nil, err
				}
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}

// NewStore creates a new store
func NewStore() *Store {
	p := newPool("127.0.0.1:6379", "")

	return &Store{
		db:       newDB("postgres://postgres:mysecretpassword@localhost:32768/postgres?sslmode=disable"),
		rds:      p,
		dispatch: NewDispatch(p),
	}
}

// Store holds references to all the databases that we need.
type Store struct {
	db       *sql.DB
	rds      *redis.Pool
	dispatch *Dispatch
}

// Start begins the process subscribing system.
func (s *Store) Start() {
	// send off the subscribe process which will listen to Redis to actually
	// broker the recording of data
	go s.ProcessSubscribe()

	// start the worker dispatcher
	s.dispatch.Start()
}

// Close finishes all resources used by the store.
func (s *Store) Close() {
	s.dispatch.Close()

	// close down the redis pool
	s.rds.Close()

	// close down the sql connection
	s.db.Close()
}

// ProcessSubscribe processes the impression data and persists it to disk.
func (s *Store) ProcessSubscribe() error {
	con := s.rds.Get()
	defer con.Close()

	log.Println("impression_writer: active")

	for {
		replies, err := redis.Values(con.Do("BLPOP", "impressions", 0))
		if err != nil {
			log.Printf("impression_writer: error: %s\n", err.Error())
			return err
		}

		log.Printf("impression_writer: got a impression\n")

		var reply []byte
		_, err = redis.Scan(replies, nil, &reply)
		if err != nil {
			log.Printf("impression_writer: error: %s\n", err.Error())
			return err
		}

		var i Impression
		err = json.Unmarshal(reply, &i)
		if err != nil {
			log.Printf("impression_writer: error: %s\n", err.Error())
			return err
		}

		log.Printf("impression_writer: recording %s\n", i.ID)

		err = s.RecordImpression(con, &i)
		if err != nil {
			log.Printf("impression_writer: error: %s\n", err.Error())
			return err
		}

		log.Printf("impression_writer: recorded %s\n", i.ID)
	}
}

// Publish pushes the impression onto the queue.
func (s *Store) Publish(i Impression) {
	s.dispatch.Queue <- i
}

// CreateAd creates the new ad and updates the lookup store.
func (s *Store) CreateAd(ad *Ad) error {
	stmt, err := s.db.Prepare("INSERT INTO ad(adid, adweight, adimg, adimgalt, adimgw, adimgh, adimglink, adtext) values($1, $2, $3, $4, $5, $6, $7, $8)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	// create a new id
	ad.ID = uuid.NewV4().String()

	_, err = stmt.Exec(ad.ID, ad.Weight, ad.Img, ad.ImgAlt, ad.ImgW, ad.ImgH, ad.ImgLink, ad.Text)
	if err != nil {
		return err
	}

	err = s.RebuildSelector()
	if err != nil {
		return err
	}

	return nil
}

// RetrieveAds returns all the ads from the service.
func (s *Store) RetrieveAds() ([]Ad, error) {
	rows, err := s.db.Query("SELECT adid, adweight, adimg, adimgalt, adimgw, adimgh, adimglink, adtext from ad")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ads []Ad
	for rows.Next() {
		var ad Ad

		err = rows.Scan(&ad.ID, &ad.Weight, &ad.Img, &ad.ImgAlt, &ad.ImgW, &ad.ImgH, &ad.ImgLink, &ad.Text)
		if err != nil {
			return nil, err
		}

		ads = append(ads, ad)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return ads, nil
}

func (s *Store) selectAdJSON(con redis.Conn) (*Ad, error) {
	rnd := rand.Float64()

	reply, err := redis.Values(con.Do("ZRANGEBYSCORE", "ADS", rnd, "+inf", "LIMIT", 0, 1))
	if err != nil {
		return nil, err
	}

	if len(reply) <= 0 {
		return nil, ErrNotFound
	}

	var adJSON string

	_, err = redis.Scan(reply, &adJSON)
	if err != nil {
		return nil, err
	}

	var ad Ad
	err = json.Unmarshal([]byte(adJSON), &ad)
	if err != nil {
		return nil, err
	}

	return &ad, nil
}

// GetRandomAd retrieves a random ad from the database.
func (s *Store) GetRandomAd() (*Ad, error) {
	con := s.rds.Get()
	defer con.Close()

	ad, err := s.selectAdJSON(con)
	if err != nil {
		return nil, err
	}

	// record the impression
	s.Publish(NewImpression(ad.ID))

	return ad, nil
}

// RecordImpression actually records the impression in the database.
func (s *Store) RecordImpression(con redis.Conn, i *Impression) error {
	stmt, err := s.db.Prepare("INSERT INTO impression(id, adid, timestamp) values($1, $2, $3)")
	if err != nil {
		return err
	}
	defer stmt.Close()

	_, err = stmt.Exec(i.ID, i.AdID, i.Timestamp)
	if err != nil {
		return err
	}

	err = con.Send("INCR", fmt.Sprintf("impressions:%s", i.AdID))
	if err != nil {
		return err
	}

	err = con.Flush()
	if err != nil {
		return err
	}

	return nil
}

// RebuildSelector rebuilds the selector for the ads.
func (s *Store) RebuildSelector() error {
	con := s.rds.Get()
	defer con.Close()

	return s.rebuildSelector(con)
}

func (s *Store) rebuildSelector(con redis.Conn) error {
	rows, err := s.db.Query("SELECT adid, adweight, adimg, adimgalt, adimgw, adimgh, adimglink, adtext from ad")
	if err != nil {
		return err
	}
	defer rows.Close()

	var weightTotal float64
	var ads []Ad
	for rows.Next() {
		var ad Ad

		err = rows.Scan(&ad.ID, &ad.Weight, &ad.Img, &ad.ImgAlt, &ad.ImgW, &ad.ImgH, &ad.ImgLink, &ad.Text)
		if err != nil {
			return err
		}

		weightTotal += float64(ad.Weight)
		ads = append(ads, ad)
	}

	if err = rows.Err(); err != nil {
		return err
	}

	if err := con.Send("DEL", "ADS"); err != nil {
		return err
	}

	var score float64
	for _, ad := range ads {
		score += float64(ad.Weight) / weightTotal
		ad.Score = score

		adBytes, err := json.Marshal(ad)
		if err != nil {
			return err
		}

		if err := con.Send("ZADD", "ADS", score, string(adBytes)); err != nil {
			return err
		}
	}

	if err := con.Flush(); err != nil {
		return err
	}

	return nil
}
