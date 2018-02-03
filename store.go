package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"time"

	"github.com/ardanlabs/kit/log"
	"github.com/garyburd/redigo/redis"
	_ "github.com/lib/pq"
	"github.com/pkg/errors"
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
func newDB(path string) (*sql.DB, error) {
	db, err := sql.Open("postgres", path)
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(SetupDBStmt)
	if err != nil {
		return nil, err
	}

	return db, nil
}

func newPool(addr string) (*redis.Pool, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, err
	}

	pool := redis.Pool{
		MaxIdle:     3,
		IdleTimeout: 240 * time.Second,
		Dial: func() (redis.Conn, error) {
			c, err := redis.Dial("tcp", u.Host)
			if err != nil {
				return nil, err
			}
			if u.User != nil {
				password, ok := u.User.Password()

				if ok {
					if _, err = c.Do("AUTH", password); err != nil {
						c.Close()
						return nil, err
					}
				}
			}
			return c, err
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}

	con := pool.Get()
	defer con.Close()

	if _, err := con.Do("PING"); err != nil {
		return nil, err
	}

	return &pool, nil
}

// NewStore creates a new store
func NewStore(postgresURL, redisURL string) (*Store, error) {
	pool, err := newPool(redisURL)
	if err != nil {
		return nil, errors.Wrap(err, "cannot connect to redis")
	}

	db, err := newDB(postgresURL)
	if err != nil {
		return nil, errors.Wrap(err, "cannot connect to postgres")
	}

	return &Store{
		db:       db,
		rds:      pool,
		dispatch: NewDispatch(pool),
	}, nil
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

// ProcessJobQueue will get a job off the queue or return an error.
func (s *Store) ProcessJobQueue(con redis.Conn) error {
	replies, err := redis.Values(con.Do("LPOP", "impressions"))
	if err != nil {
		return errors.Wrap(err, "could not pop the job")
	}

	log.Dev("main", "ProcessSubscribe", "got a impression")

	var reply []byte
	_, err = redis.Scan(replies, nil, &reply)
	if err != nil {
		return errors.Wrap(err, "could scan the job details")
	}

	var i Impression
	err = json.Unmarshal(reply, &i)
	if err != nil {
		return errors.Wrap(err, "could not unmarshal the job")
	}

	log.Dev("main", "ProcessSubscribe", "recording %s", i.ID)

	err = s.RecordImpression(con, &i)
	if err != nil {
		return errors.Wrap(err, "could not record the impression")
	}

	log.Dev("main", "ProcessSubscribe", "recorded %s", i.ID)
	return nil
}

// ProcessSubscribe processes the impression data and persists it to disk.
func (s *Store) ProcessSubscribe() error {
	con := s.rds.Get()
	defer con.Close()

	log.Dev("main", "ProcessSubscribe", "Started")

	for {

		// Process all the jobs in the queue until it's empty.
		for {
			if err := s.ProcessJobQueue(con); err != nil {
				if errors.Cause(err) == redis.ErrNil {
					log.Dev("main", "ProcessSubscribe", "list empty")
					break
				}

				return err
			}
		}

		// Job queue was empty, sleep for 2 seconds after we tried to get the last
		// impression.
		time.Sleep(2 * time.Second)
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

	// Create a pipeline transaction for the update operation.
	con.Send("MULTI")
	con.Send("DEL", "ADS")

	// Compile the scores as we go.
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

	// Execute the transaction.
	if _, err := con.Do("EXEC"); err != nil {
		return err
	}

	return nil
}
