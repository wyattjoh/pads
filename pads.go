package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/template"
	"time"

	"github.com/ardanlabs/kit/log"
	"github.com/julienschmidt/httprouter"
)

// ScriptTemplate is the template for the script to send to the client via the
// serve action.
const ScriptTemplate = `
var advert = '';

{{if .ImgLink }}
advert+='<a href="{{js .ImgLink}}">';
{{end}}
{{if .Img}}
advert+='<img class="ad" {{if .ImgAlt}}alt="{{js .ImgAlt}}" {{end}}src="{{js .Img}}" {{if .ImgW}}width="{{js .ImgW}}"{{end}}{{if .ImgH}} height="{{js .ImgH}}"{{end}}/>';
{{end}}
{{if .ImgLink}}
advert+='</a>';
{{end}}
{{if .Text}}
advert+='<p class="ads">';
advert+='{{js .Text}}';
advert+='</p>';
{{end}}

document.write(advert);
`

// HTMLTemplate is the template used to render the ad out to html.
const HTMLTemplate = `
<!DOCTYPE html>
<html>
  <head>
    <meta charset="utf-8">
    <title></title>
  </head>
  <body>
		{{if .Img}}
		{{if .ImgLink }}<a href="{{html .ImgLink}}">{{end}}
			<img class="ad" {{if .ImgAlt}}alt="{{html .ImgAlt}}" {{end}}src="{{html .Img}}" {{if .ImgW}}width="{{html .ImgW}}"{{end}}{{if .ImgH}} height="{{html .ImgH}}"{{end}}/>
		{{if .ImgLink}}</a>{{end}}
		{{if .Text -}}
		<p class="ads">{{.Text}}</p>
		{{- end}}
		{{end}}
  </body>
</html>`

const (

	// PublishWorkerCount is the count of workers that are processing publish
	// requests.
	PublishWorkerCount = 5

	// PublishBuffer is the amount of spaces that each worker should be
	// able to queue before it is full.
	PublishBuffer = 1000

	// PublishWorkerBuffer is the amount of Impressions that the worker will
	// collect before it flushes it to the queue
	PublishWorkerBuffer = 50

	// PublishWorkerTimeout is the timeout that if reached, will force a buffer
	// flush immediatly.
	PublishWorkerTimeout = time.Millisecond * 500
)

// ShowAdJSEndpoint serves the advertisement via javascript
func ShowAdJSEndpoint(s *Store) httprouter.Handle {
	// Create a new template and parse the letter into it.
	t := template.Must(template.New("script").Parse(ScriptTemplate))

	return httprouter.Handle(func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Keep-Alive", "timeout=5, max=100")
		w.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := t.Execute(w, ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	})
}

// ShowAdHTMLEndpoint serves the advertisement via html
func ShowAdHTMLEndpoint(s *Store) httprouter.Handle {
	// Create a new template and parse the letter into it.
	t := template.Must(template.New("html").Parse(HTMLTemplate))

	return httprouter.Handle(func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Keep-Alive", "timeout=5, max=100")
		w.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := t.Execute(w, ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	})
}

// ShowAdJSONEndpoint serves the advertisement via json
func ShowAdJSONEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Keep-Alive", "timeout=5, max=100")
		w.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
				return
			}

			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	})
}

// CreateAdEndpoint creates a new ad.
func CreateAdEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")

		var ad Ad
		if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := s.CreateAd(&ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(ad); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	})
}

// RetrieveAdsEndpoint lists the ads in the system
func RetrieveAdsEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Content-Type", "application/json")

		ads, err := s.RetrieveAds()
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		if err := json.NewEncoder(w).Encode(ads); err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
	})
}

// StartServer starts up the http server.
func StartServer(host, port string, s *Store) {
	mux := httprouter.New()

	mux.GET("/ad.js", ShowAdJSEndpoint(s))
	mux.GET("/ad.json", ShowAdJSONEndpoint(s))
	mux.GET("/ad.html", ShowAdHTMLEndpoint(s))
	mux.GET("/ad", ShowAdHTMLEndpoint(s))

	mux.POST("/api/v1/advertisement", CreateAdEndpoint(s))
	mux.GET("/api/v1/advertisement", RetrieveAdsEndpoint(s))

	bind := fmt.Sprintf("%s:%s", host, port)

	log.User("main", "StartServer", "Listening on %s", bind)

	go func() {
		if err := http.ListenAndServe(bind, mux); err != nil {
			log.Fatal("main", "cannot listen and serve: %s", err.Error())
		}
	}()
}

// StartStoreProcessor will startup all the goroutines used by the store.
func StartStoreProcessor(s *Store) {

	// start the dispatcher
	s.Start()
}

// StopStoreProcessor will halt all processing on the processing channels.
func StopStoreProcessor(s *Store) {
	log.Dev("main", "StopStoreProcessor", "Started")

	// close down the store.
	s.Close()

	log.Dev("main", "StopStoreProcessor", "Finished")
}

// StartSmartShutdown will wait until an inturrupt is issued and then shutdown
// all the workers.
func StartSmartShutdown(s *Store) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)

	// this will wait until an interrupt or a sigterm is caught
	<-c
}

func main() {
	postgresURL := flag.String("postgres", "postgres://postgres:mysecretpassword@localhost:32768/postgres?sslmode=disable", "the url including auth for the postgres server")
	redisURL := flag.String("redis", "redis://127.0.0.1:6379", "the url including auth for the redis server")
	verbose := flag.Bool("verbose", false, "enable verbose logging")
	host := flag.String("host", "127.0.0.1", "host to listen on")
	port := flag.String("port", "8080", "port number to listen on")

	flag.Parse()

	log.Init(os.Stderr, func() int {
		if *verbose {
			return log.DEV
		}

		return log.USER
	}, log.Ldefault)

	store, err := NewStore(*postgresURL, *redisURL)
	if err != nil {
		log.Fatal("main", "cannot create the store: %s", err.Error())
	}

	defer StopStoreProcessor(store)

	if err := store.RebuildSelector(); err != nil {
		log.Fatal("main", "cannot rebuild the selector: %s", err.Error())
	}

	// start up the store's workers
	StartStoreProcessor(store)

	// setup the http server
	StartServer(*host, *port, store)

	// setup the smart shutdown
	StartSmartShutdown(store)
}
