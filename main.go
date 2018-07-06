package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"./api"
	"./background"
	"./handlers"
	"./utils"

	"github.com/ad/gocc/selfupdate"
	"github.com/gorilla/csrf"
	"github.com/gorilla/mux"
	"github.com/nu7hatch/gouuid"
)

const version = "0.3.0"

var port = flag.String("port", "9000", "Port to listen on")
var serveruuid, _ = uuid.NewV4()
var fqdn = utils.FQDN()

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
	handlers.Version = version
}

func main() {
	log.Printf("Started version %s at %s", version, fqdn)

	go selfupdate.StartSelfupdate("ad/gocc", version, fqdn)

	resetProcessingTicker := time.NewTicker(60 * time.Second)
	go func(resetProcessingTicker *time.Ticker) {
		for {
			select {
			case <-resetProcessingTicker.C:
				background.ResetProcessing()
			}
		}
	}(resetProcessingTicker)

	checkAliveTicker := time.NewTicker(60 * time.Second)
	go func(checkAliveTicker *time.Ticker) {
		for {
			select {
			case <-checkAliveTicker.C:
				background.CheckAlive()
			}
		}
	}(checkAliveTicker)

	resendRepeatableTicker := time.NewTicker(60 * time.Second)
	go func(resendRepeatableTicker *time.Ticker) {
		for {
			select {
			case <-resendRepeatableTicker.C:
				background.ResendRepeatable()
			}
		}
	}(resendRepeatableTicker)

	getActiveDestinationsTicker := time.NewTicker(120 * time.Second)
	go func(getActiveDestinationsTicker *time.Ticker) {
		for {
			select {
			case <-getActiveDestinationsTicker.C:
				utils.GetActiveDestinations()
			}
		}
	}(getActiveDestinationsTicker)

	go background.ResendOffline()

	r := mux.NewRouter()

	r.Handle("/", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.GetHandler))).Methods("GET")
	r.Handle("/auth", http.HandlerFunc(handlers.AuthHandler))

	r.Handle("/dispatch/", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.DispatchHandler))).Methods("GET")
	r.Handle("/version", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ShowVersion))).Methods("GET")
	r.Handle("/task/create", handlers.Throttle(time.Minute, 10, http.HandlerFunc(handlers.ShowCreateForm))).Methods("GET")
	r.Handle("/task/create", handlers.Throttle(time.Minute, 10, http.HandlerFunc(handlers.TaskCreateHandler))).Methods("POST")
	r.Handle("/task/my", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ShowMyTasks)))
	r.Handle("/task/repeatable", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ShowRepeatableTasks))).Methods("GET")
	r.Handle("/task/repeatable/remove", handlers.Throttle(time.Minute, 10, http.HandlerFunc(handlers.TaskRepeatableRemoveHandler))).Methods("POST")

	r.Handle("/zond/task/block", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.TaskBlockHandler)))
	r.Handle("/zond/task/result", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.TaskResultHandler)))

	r.Handle("/zond/pong", handlers.Throttle(time.Minute, 5, http.HandlerFunc(handlers.ZondPong)))
	r.Handle("/zond/create", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ZondCreateHandler))).Methods("POST")
	r.Handle("/zond/my", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ShowMyZonds)))
	r.Handle("/zond/sub", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ZondSub)))
	r.Handle("/zond/unsub", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.ZondUnsub)))

	r.Handle("/user", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.UserInfoHandler))).Methods("GET")
	r.Handle("/user/auth", handlers.Throttle(time.Minute, 60, http.HandlerFunc(handlers.UserAuthHandler)))
	r.Handle("/recover", handlers.Throttle(time.Minute, 3, http.HandlerFunc(handlers.UserRecoverHandler)))
	r.Handle("/reset", handlers.Throttle(time.Minute, 3, http.HandlerFunc(handlers.UserResetHandler)))
	r.Handle("/login", handlers.Throttle(time.Minute, 5, http.HandlerFunc(handlers.UserLoginHandler)))
	r.Handle("/register", handlers.Throttle(time.Minute, 5, http.HandlerFunc(handlers.UserRegisterHandler)))

	r.Handle("/api/task", handlers.Throttle(time.Minute, 10, http.HandlerFunc(api.TaskCreateHandler)))

	r.NotFoundHandler = http.HandlerFunc(handlers.NotFound)

	CSRF := csrf.Protect(
		[]byte(utils.RandStr(32)),
		csrf.FieldName("token"),
		csrf.Secure(false), // NB: REMOVE IN PRODUCTION!
		csrf.Path("/"),
	)

	skipCheck := func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			for _, path := range []string{"/zond/task", "/zond/pong"} {
				if strings.HasPrefix(r.URL.Path, path) {
					r = csrf.UnsafeSkipCheck(r)
				}
			}
			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}

	loggingHandler := func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			t := time.Now()
			h.ServeHTTP(w, r)
			elapsed := time.Since(t)
			fmt.Printf(
				"%s - %s%s - [%s] \"%s %s %s\" %s\n",
				r.Header.Get("X-Forwarded-For"),
				r.Header.Get("X-Zonduuid"),
				r.Header.Get("X-Forwarded-User"),
				t.Format("02/Jan/2006:15:04:05 -0700"),
				r.Method,
				r.RequestURI,
				r.Proto,
				elapsed,
			)
		}
		return http.HandlerFunc(fn)
	}

	log.Printf("listening on port %s", *port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+*port, skipCheck(CSRF(loggingHandler(r)))))
}
