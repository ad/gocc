package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ad/gocc/ccredis"
	"github.com/ad/gocc/utils"

	"github.com/gorilla/csrf"

	"github.com/ulule/limiter"
	"github.com/ulule/limiter/drivers/middleware/stdlib"
	"github.com/ulule/limiter/drivers/store/memory"
)

var Fqdn = utils.FQDN()
var Version = ""

func Throttle(period time.Duration, limit int64, f http.Handler) http.Handler {
	rateLimitStore := memory.NewStore()
	rate := limiter.Rate{
		Period: period,
		Limit:  limit,
	}
	rateLimiter := stdlib.NewMiddleware(limiter.New(rateLimitStore, rate),
		stdlib.WithForwardHeader(true))
	return rateLimiter.Handler(f)
}

func ZondAuth(f http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var uuid = r.Header.Get("X-ZondUuid")

		if len(uuid) != 36 {
			http.Error(w, "Not authorized", 401)
			return
		}

		isMember, _ := ccredis.Client.SIsMember("zonds", uuid).Result()
		if !isMember {
			http.Error(w, "Not authorized", 401)
			return
		}

		// TODO: check zond state

		f.ServeHTTP(w, r)
	}
}

func MngrAuth(f http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var uuid = r.Header.Get("X-MngrUuid")

		if len(uuid) != 36 {
			http.Error(w, "Not authorized", 401)
			return
		}

		isMember, _ := ccredis.Client.SIsMember("mngrs", uuid).Result()
		if !isMember {
			http.Error(w, "Not authorized", 401)
			return
		}

		// TODO: check zond state

		f.ServeHTTP(w, r)
	}
}

func NotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(404)
	// tmpl, _ := templ.New("error404", Asset).Parse("error404.html")
	// tmpl.Execute(w, nil)
}

func TokenHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-CSRF-Token", csrf.Token(r))
	w.Write([]byte(""))
}

func GetHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("GET done", r)

	users, _ := ccredis.Client.SMembers("Zond-online").Result()
	usersCount, _ := ccredis.Client.SCard("Zond-online").Result()
	log.Println("active users", users, usersCount)

	jsonBody, err := json.Marshal(users)
	if err != nil {
		http.Error(w, "Error converting results to json",
			http.StatusInternalServerError)
	}
	w.Write(jsonBody)
}

func DispatchHandler(w http.ResponseWriter, r *http.Request, gogeoaddr *string) {
	var uuid = r.Header.Get("X-ZondUuid")
	var mngruuid = r.Header.Get("X-MngrUuid")
	var ip = r.Header.Get("X-Forwarded-For")
	if len(uuid) == 36 {
		isMember, _ := ccredis.Client.SIsMember("zonds", uuid).Result()
		if isMember {
			var add = utils.IPToWSChannels(ip, gogeoaddr)
			log.Println("/internal/sub/zond:" + uuid + "," + add + "," + ip)
			w.Header().Add("X-Accel-Redirect", "/internal/sub/zond:"+uuid+","+add+","+ip)
		} else {
			log.Println("zond uuid not found: " + uuid + ", ip" + ip)
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Add("X-Accel-Redirect", "/404")
			w.Header().Add("X-Accel-Buffering", "no")
			w.Write([]byte(""))
			return
		}
	} else if len(mngruuid) == 36 {
		isMember, _ := ccredis.Client.SIsMember("mngrs", mngruuid).Result()
		if isMember {
			log.Println("/internal/sub/mngrtasks,mngr" + mngruuid + "," + ip)
			w.Header().Add("X-Accel-Redirect", "/internal/sub/mngrtasks,mngr"+mngruuid+","+ip)
		} else {
			log.Println("mngr uuid not found: " + mngruuid + ", ip" + ip)
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Add("X-Accel-Redirect", "/404")
			w.Header().Add("X-Accel-Buffering", "no")
			w.Write([]byte(""))
			return
		}
	} else {
		log.Println("/internal/sub/destinations,tasks/done," + ip + "," + Fqdn)
		w.Header().Add("X-Accel-Redirect", "/internal/sub/destinations,tasks/done,"+ip+","+Fqdn)
	}

	w.Header().Add("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func ShowVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, Version)
}
