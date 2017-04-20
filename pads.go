package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"text/template"
	"time"

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

	return httprouter.Handle(func(rw http.ResponseWriter, r *http.Request, p httprouter.Params) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Content-Type", "text/javascript")
		rw.Header().Set("Keep-Alive", "timeout=5, max=100")
		rw.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				rw.WriteHeader(404)
				return
			}

			panic(err)
		}

		if err := t.Execute(rw, ad); err != nil {
			panic(err)
		}
	})
}

// ShowAdHTMLEndpoint serves the advertisement via html
func ShowAdHTMLEndpoint(s *Store) httprouter.Handle {
	// Create a new template and parse the letter into it.
	t := template.Must(template.New("html").Parse(HTMLTemplate))

	return httprouter.Handle(func(rw http.ResponseWriter, r *http.Request, p httprouter.Params) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Content-Type", "text/html")
		rw.Header().Set("Keep-Alive", "timeout=5, max=100")
		rw.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				rw.WriteHeader(404)
				return
			}

			panic(err)
		}

		if err := t.Execute(rw, ad); err != nil {
			panic(err)
		}
	})
}

// ShowAdJSONEndpoint serves the advertisement via json
func ShowAdJSONEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(rw http.ResponseWriter, r *http.Request, p httprouter.Params) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Content-Type", "application/json")
		rw.Header().Set("Keep-Alive", "timeout=5, max=100")
		rw.Header().Set("Connection", "Keep-Alive")

		ad, err := s.GetRandomAd()
		if err != nil {
			if err == ErrNotFound {
				rw.WriteHeader(404)
				return
			}

			panic(err)
		}

		if err := json.NewEncoder(rw).Encode(ad); err != nil {
			panic(err)
		}
	})
}

// CreateAdEndpoint creates a new ad.
func CreateAdEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(rw http.ResponseWriter, r *http.Request, p httprouter.Params) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Content-Type", "application/json")

		var ad Ad
		if err := json.NewDecoder(r.Body).Decode(&ad); err != nil {
			panic(err)
		}

		if err := s.CreateAd(&ad); err != nil {
			panic(err)
		}

		if err := json.NewEncoder(rw).Encode(ad); err != nil {
			panic(err)
		}
	})
}

// RetrieveAdsEndpoint lists the ads in the system
func RetrieveAdsEndpoint(s *Store) httprouter.Handle {
	return httprouter.Handle(func(rw http.ResponseWriter, r *http.Request, p httprouter.Params) {
		rw.Header().Set("Access-Control-Allow-Origin", "*")
		rw.Header().Set("Content-Type", "application/json")

		ads, err := s.RetrieveAds()
		if err != nil {
			panic(err)
		}

		if err := json.NewEncoder(rw).Encode(ads); err != nil {
			panic(err)
		}
	})
}

// StartServer starts up the http server.
func StartServer(s *Store) {
	mux := httprouter.New()

	mux.GET("/ad.js", ShowAdJSEndpoint(s))
	mux.GET("/ad.json", ShowAdJSONEndpoint(s))
	mux.GET("/ad.html", ShowAdHTMLEndpoint(s))
	mux.GET("/ad", ShowAdHTMLEndpoint(s))

	mux.POST("/api/v1/advertisement", CreateAdEndpoint(s))
	mux.GET("/api/v1/advertisement", RetrieveAdsEndpoint(s))

	log.Println("Listening on :8080")

	go func() {
		if err := http.ListenAndServe(":8080", mux); err != nil {
			panic(err)
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
	log.Println("Shutdown started")

	// close down the store.
	s.Close()

	log.Println("Shutdown reached.")
}

// StartSmartShutdown will wait until an inturrupt is issued and then shutdown
// all the workers.
func StartSmartShutdown(s *Store) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	signal.Notify(c, syscall.SIGTERM)

	// this will wait until an inturrupt or a sigterm is caught
	<-c
}

func main() {
	// log.SetOutput(ioutil.Discard)

	s := NewStore()
	defer StopStoreProcessor(s)

	err := s.RebuildSelector()
	if err != nil {
		panic(err)
	}

	// start up the store's workers
	StartStoreProcessor(s)

	// setup the http server
	StartServer(s)

	// setup the smart shutdown
	StartSmartShutdown(s)
}
