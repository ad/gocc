package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	pagination "github.com/AndyEverLie/go-pagination-bootstrap"
	"github.com/ad/gocc/mail"
	"github.com/ad/gocc/selfupdate"
	"github.com/ad/gocc/utils"
	templ "github.com/arschles/go-bindata-html-template"
	"github.com/go-redis/redis"
	"github.com/gorilla/csrf"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/gorilla/securecookie"
	"github.com/nu7hatch/gouuid"
	"github.com/ulule/limiter"
	"github.com/ulule/limiter/drivers/middleware/stdlib"
	"github.com/ulule/limiter/drivers/store/memory"
)

const version = "0.2.5"

type Action struct {
	Creator    string `json:"creator"`
	ZondUuid   string `json:"zond"`
	Action     string `json:"action"`
	Param      string `json:"param"`
	Result     string `json:"result"`
	Uuid       string `json:"uuid"`
	ParentUUID string `json:"parent"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
	Target     string `json:"target"`
	Repeat     string `json:"repeat"`
}

type Result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Zond struct {
	Creator string `json:"creator"`
	Uuid    string `json:"uuid"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
}

type Channels struct {
	Action    string   `json:"action"`
	Zonds     []string `json:"zonds"`
	Countries []string `json:"countries"`
	Cities    []string `json:"cities"`
	ASNs      []string `json:"asns"`
}

type ErrorMessage struct {
	Text  string
	Color string
}

var port = flag.String("port", "9000", "Port to listen on")
var serveruuid, _ = uuid.NewV4()
var fqdn = utils.FQDN()

var (
	nsCookieName         = "NSLOGIN"
	nsCookieHashKey      = []byte("SECURE_COOKIE_HASH_KEY")
	nsRedirectCookieName = "NSREDIRECT"
)

func main() {
	log.Printf("Started version %s at %s", version, fqdn)

	go selfupdate.StartSelfupdate("ad/gocc", version)

	resetProcessingTicker := time.NewTicker(60 * time.Second)
	go func(resetProcessingTicker *time.Ticker) {
		for {
			select {
			case <-resetProcessingTicker.C:
				resetProcessing()
			}
		}
	}(resetProcessingTicker)

	checkAliveTicker := time.NewTicker(60 * time.Second)
	go func(checkAliveTicker *time.Ticker) {
		for {
			select {
			case <-checkAliveTicker.C:
				checkAlive()
			}
		}
	}(checkAliveTicker)

	resendRepeatableTicker := time.NewTicker(60 * time.Second)
	go func(resendRepeatableTicker *time.Ticker) {
		for {
			select {
			case <-resendRepeatableTicker.C:
				resendRepeatable()
			}
		}
	}(resendRepeatableTicker)

	getActiveDestinationsTicker := time.NewTicker(120 * time.Second)
	go func(getActiveDestinationsTicker *time.Ticker) {
		for {
			select {
			case <-getActiveDestinationsTicker.C:
				getActiveDestinations()
			}
		}
	}(getActiveDestinationsTicker)

	go resendOffline()

	r := mux.NewRouter()

	r.Handle("/", throttle(time.Minute, 60, http.HandlerFunc(GetHandler))).Methods("GET")
	r.Handle("/dispatch/", throttle(time.Minute, 60, http.HandlerFunc(DispatchHandler)))
	r.Handle("/version", throttle(time.Minute, 60, http.HandlerFunc(ShowVersion))).Methods("GET")
	r.Handle("/task/create", throttle(time.Minute, 10, http.HandlerFunc(TaskCreateHandler)))
	r.Handle("/task/my", throttle(time.Minute, 60, http.HandlerFunc(ShowMyTasks)))
	r.Handle("/task/repeatable", throttle(time.Minute, 60, http.HandlerFunc(ShowRepeatableTasks)))
	r.Handle("/task/repeatable/remove", throttle(time.Minute, 10, http.HandlerFunc(TaskRepeatableRemoveHandler)))
	r.Handle("/zond/task/block", throttle(time.Minute, 60, http.HandlerFunc(TaskBlockHandler)))
	r.Handle("/zond/task/result", throttle(time.Minute, 60, http.HandlerFunc(TaskResultHandler)))
	r.Handle("/zond/pong", throttle(time.Minute, 5, http.HandlerFunc(ZondPong)))
	r.Handle("/zond/create", throttle(time.Minute, 60, http.HandlerFunc(ZondCreateHandler)))
	r.Handle("/zond/my", throttle(time.Minute, 60, http.HandlerFunc(ShowMyZonds)))
	r.Handle("/zond/sub", throttle(time.Minute, 60, http.HandlerFunc(ZondSub)))
	r.Handle("/zond/unsub", throttle(time.Minute, 60, http.HandlerFunc(ZondUnsub)))
	r.Handle("/user", throttle(time.Minute, 60, http.HandlerFunc(userInfoHandler))).Methods("GET")
	r.Handle("/user/auth", throttle(time.Minute, 60, http.HandlerFunc(UserAuthHandler)))
	r.Handle("/recover", throttle(time.Minute, 3, http.HandlerFunc(userRecoverHandler)))
	r.Handle("/reset", throttle(time.Minute, 3, http.HandlerFunc(userResetHandler)))
	r.Handle("/login", throttle(time.Minute, 5, http.HandlerFunc(UserLoginHandler)))
	r.Handle("/register", throttle(time.Minute, 5, http.HandlerFunc(userRegisterHandler)))
	r.Handle("/auth", http.HandlerFunc(authHandler))

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

	log.Printf("listening on port %s", *port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+*port, skipCheck(CSRF(handlers.CombinedLoggingHandler(os.Stdout, r)))))
}

func throttle(period time.Duration, limit int64, f http.Handler) http.Handler {
	rateLimitStore := memory.NewStore()
	rate := limiter.Rate{
		Period: period,
		Limit:  limit,
	}
	rateLimiter := stdlib.NewMiddleware(limiter.New(rateLimitStore, rate),
		stdlib.WithForwardHeader(true))
	return rateLimiter.Handler(f)
}

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	flag.Parse()
	rand.Seed(time.Now().UnixNano())
}

var client = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
})

func GetHandler(w http.ResponseWriter, r *http.Request) {
	log.Println("GET done", r)

	users, _ := client.SMembers("Zond-online").Result()
	usersCount, _ := client.SCard("Zond-online").Result()
	log.Println("active users", users, usersCount)

	jsonBody, err := json.Marshal(users)
	if err != nil {
		http.Error(w, "Error converting results to json",
			http.StatusInternalServerError)
	}
	w.Write(jsonBody)
}

func DispatchHandler(w http.ResponseWriter, r *http.Request) {
	// log.Println("DispatchHandler", r.Header.Get("X-Zonduuid"), r.Header.Get("X-Forwarded-For"))

	var uuid = r.Header.Get("X-Zonduuid")
	var ip = r.Header.Get("X-Forwarded-For")
	if len(uuid) > 0 {
		var add = utils.IPToWSChannels(ip)
		log.Println("/internal/sub/zond:" + uuid + "," + add + "," + ip)
		w.Header().Add("X-Accel-Redirect", "/internal/sub/zond:"+uuid+","+add+","+ip)
		w.Header().Add("X-Accel-Buffering", "no")
	} else {
		// log.Println("/internal/sub/tasks/done," + ip)

		w.Header().Add("X-Accel-Redirect", "/internal/sub/destinations,tasks/done,"+ip)
		w.Header().Add("X-Accel-Buffering", "no")
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func ZondCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		name := r.FormValue("name")

		u, _ := uuid.NewV4()
		var Uuid = u.String()
		var msec = time.Now().Unix()

		if len(name) == 0 {
			name = Uuid
		}

		client.SAdd("zonds", Uuid)

		userUuid, _ := client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
		if userUuid == "" {
			u, _ := uuid.NewV4()
			userUuid = u.String()
			client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
		}

		zond := Zond{Uuid: Uuid, Name: name, Created: msec, Creator: userUuid}
		js, _ := json.Marshal(zond)

		client.Set("zonds/"+Uuid, string(js), 0)
		client.SAdd("user/zonds/"+userUuid, Uuid)

		log.Println("Zond created", Uuid)

		if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
			// w.Header().Set("X-CSRF-Token", csrf.Token(r))
			fmt.Fprintf(w, `{"status": "ok", "uuid": "%s"}`, Uuid)
		} else {
			ShowCreateForm(w, r, Uuid)
		}
	} else {
		ShowCreateForm(w, r, "")
	}
}

func TaskCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ip := r.FormValue("ip")

		if len(ip) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing required IP param."))
			return
		}

		dest := r.FormValue("dest")
		destination := "tasks"
		if len(dest) > 4 && strings.Count(dest, ":") == 2 {
			target := strings.Join(strings.Split(dest, ":")[2:], ":")
			if strings.HasPrefix(dest, "zond:uuid:") {
				test, _ := client.SIsMember("Zond-online", target).Result()
				if test {
					destination = "zond:" + target
				}
			} else if strings.HasPrefix(dest, "zond:city:") {
				// FIXME: check if destination is available
				destination = "City:" + target
			} else if strings.HasPrefix(dest, "zond:country:") {
				// FIXME: check if destination is available
				destination = "Country:" + target
			} else if strings.HasPrefix(dest, "zond:asn:") {
				// FIXME: check if destination is available
				destination = "ASN:" + target
			}
		}

		taskType := r.FormValue("type")
		taskTypes := map[string]bool{
			"ping":       true,
			"head":       true,
			"dns":        true,
			"traceroute": true,
		}

		repeatType := r.FormValue("repeat")
		repeatTypes := map[string]int{
			"5min":   300,
			"10min":  600,
			"30min":  1800,
			"1hour":  3600,
			"3hour":  10800,
			"6hour":  21600,
			"12hour": 43200,
			"1day":   86400,
			"1week":  604800,
		}

		if repeatTypes[repeatType] <= 0 {
			repeatType = "single"
		}

		if taskTypes[taskType] {
			u, _ := uuid.NewV4()
			var Uuid = u.String()
			var msec = time.Now().Unix()

			client.SAdd("tasks-new", Uuid)

			users, _ := client.SMembers("tasks-new").Result()
			usersCount, _ := client.SCard("tasks-new").Result()
			log.Println("tasks-new", users, usersCount)

			userUuid, _ := client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
			if userUuid == "" {
				u, _ := uuid.NewV4()
				userUuid = u.String()
				client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
			}

			action := Action{Action: taskType, Param: ip, Uuid: Uuid, Created: msec, Creator: userUuid, Target: destination, Repeat: repeatType}
			js, _ := json.Marshal(action)

			client.Set("task/"+Uuid, string(js), 0)
			client.SAdd("user/tasks/"+userUuid, Uuid)
			if repeatType != "single" {
				t := time.Now()
				tnew := t.Add(time.Duration(repeatTypes[repeatType]) * time.Second).Unix()
				t300 := (tnew - (tnew % 300))
				log.Println("next start will be at ", strconv.FormatInt(t300, 10))

				client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300, 10), string(js))
			}

			go post("http://127.0.0.1:80/pub/"+destination, string(js))

			log.Println(ip, taskType, Uuid)
		} else {
			// w.Header().Set("X-CSRF-Token", csrf.Token(r))
			fmt.Fprintf(w, `{"status": "error", "error": "wrong task type"}`)
			return
		}
	}

	if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
		// w.Header().Set("X-CSRF-Token", csrf.Token(r))
		fmt.Fprintf(w, `{"status": "ok"}`)
	} else {
		ShowCreateForm(w, r, "")
	}

}

func TaskBlockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.ZondUuid, "wants to", t.Action, t.Uuid)
			zondBusy, err := client.SIsMember("zond-busy", t.ZondUuid).Result()
			if (err != nil) || (zondBusy != true) {
				count := client.SRem("tasks-new", t.Uuid)
				if count.Val() == int64(1) {
					client.SAdd("tasks-process", t.ZondUuid+"/"+t.Uuid)
					client.SAdd("zond-busy", t.ZondUuid)
					client.Set(t.ZondUuid+"/"+t.Uuid+"/processing", "1", 60*time.Second)
					log.Println(t.ZondUuid, `{"status": "ok", "message": "ok"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)
				} else {
					log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				}
			} else {
				log.Println(`{"status": "error", "message": "only one task at time is allowed"}`)
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "error", "message": "only one task at time is allowed"}`)
			}
		}
	} else {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func TaskResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.ZondUuid, "wants to", t.Action, t.Uuid)
			client.SRem("zond-busy", t.ZondUuid)

			if t.Action == "result" {
				taskProcessing, err := client.SIsMember("tasks-process", t.ZondUuid+"/"+t.Uuid).Result()
				if (err != nil) || (taskProcessing != true) {
					log.Println(`{"status": "error", "message": "task not found"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				} else {
					count := client.SRem("tasks-process", t.ZondUuid+"/"+t.Uuid)
					if count.Val() == int64(1) {
						client.SAdd("tasks-done", t.ZondUuid+"/"+t.Uuid+"/"+t.Result)
						log.Println(t.ZondUuid, `{"status": "ok", "message": "ok"}`)
						// w.Header().Set("X-CSRF-Token", csrf.Token(r))
						fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)

						js, _ := client.Get("task/" + t.Uuid).Result()
						// log.Println(js)
						var task Action
						err = json.Unmarshal([]byte(js), &task)
						if err != nil {
							log.Println(err.Error())
						}
						task.Result = t.Result
						task.ZondUuid = t.ZondUuid
						task.Updated = time.Now().Unix()

						jsonBody, err := json.Marshal(task)
						if err != nil {
							http.Error(w, "Error converting results to json",
								http.StatusInternalServerError)
						}
						client.Set("task/"+t.Uuid, jsonBody, 0)
						go post("http://127.0.0.1:80/pub/tasks/done", string(jsonBody))
					} else {
						log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
						// w.Header().Set("X-CSRF-Token", csrf.Token(r))
						fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
					}
				}
			}
		}
	} else {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func resendOffline() {
	tasks, _ := client.SMembers("tasks-new").Result()

	if len(tasks) > 0 {
		log.Println("active tasks", tasks)
		for _, task := range tasks {
			js, _ := client.Get("task/" + task).Result()

			var action Action
			err := json.Unmarshal([]byte(js), &action)
			if err != nil {
				log.Println(err.Error())
			} else {
				go post("http://127.0.0.1:80/pub/"+action.Target, string(js))
				log.Println(action)
			}
		}
	}
}

func resendRepeatable() {
	t := time.Now().Unix()
	t300 := strconv.FormatInt(t-(t%300), 10) // modulo to nearest 5-minutes interval, like Mon, 02 Jul 2018 19:55:00 GMT

	tasks, _ := client.SMembers("tasks-repeatable-" + t300).Result()

	if len(tasks) > 0 {
		log.Println("repeatable tasks", tasks)
		for _, task := range tasks {
			var action Action
			err := json.Unmarshal([]byte(task), &action)
			if err != nil {
				log.Println(err.Error())
			} else {
				if len(action.ParentUUID) == 0 {
					action.ParentUUID = action.Uuid
				}
				u, _ := uuid.NewV4()
				var Uuid = u.String()
				action.Uuid = Uuid
				action.Created = t - (t % 300)
				client.SAdd("tasks-new", Uuid)

				js, _ := json.Marshal(action)

				client.Set("task/"+Uuid, string(js), 0)
				client.SAdd("user/tasks/"+action.Creator, Uuid)

				t := time.Now()

				repeatTypes := map[string]int{
					"5min":   300,
					"10min":  600,
					"30min":  1800,
					"1hour":  3600,
					"3hour":  10800,
					"6hour":  21600,
					"12hour": 43200,
					"1day":   86400,
					"1week":  604800,
				}
				tnew := t.Add(time.Duration(repeatTypes[action.Repeat]) * time.Second).Unix()
				t300new := (tnew - (tnew % 300))
				log.Println("next start will be at ", strconv.FormatInt(t300new, 10))

				client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300new, 10), string(js))

				go post("http://127.0.0.1:80/pub/"+action.Target, string(js))

				client.SRem("tasks-repeatable-"+t300, task)
			}
		}
	}
}

func TaskRepeatableRemoveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		uuid := r.FormValue("uuid")

		if len(uuid) != 36 || strings.Count(uuid, "-") != 4 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing required UUID param."))
			return
		}

		titles, _ := client.Keys("tasks-repeatable-*").Result()
		count := len(titles)
		// log.Println(count, titles)

		if count > 0 {
			var keys []string
			var err error
			for _, val := range titles {
				keys, _, err = client.SScan(val, 0, "", 0).Result()
				if err != nil {
					log.Println(err)
				} else {
					// log.Println(keys)
					for _, item := range keys {
						var t Action
						err := json.Unmarshal([]byte(item), &t)
						if err != nil {
							log.Println(err.Error())
						}
						if t.Uuid == uuid {
							client.SRem(val, item)
						}
					}
				}
			}
		}
	}

	if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
		fmt.Fprintf(w, `{"status": "ok"}`)
	} else {
		ShowRepeatableTasks(w, r)
	}
}

func resetProcessing() {
	tasks, _ := client.SMembers("tasks-process").Result()
	if len(tasks) > 0 {
		for _, task := range tasks {
			s := strings.Split(task, "/")
			ZondUuid, taskUuid := s[0], s[1]

			tp, _ := client.Get(task + "/processing").Result()
			if tp != "1" {
				log.Println("Removed outdated task", tp, ZondUuid, taskUuid)
				client.SRem("zond-busy", ZondUuid)
				client.SRem("tasks-process", task)

				client.SAdd("tasks-new", taskUuid)

				js, _ := client.Get("task/" + taskUuid).Result()
				var action Action
				err := json.Unmarshal([]byte(js), &action)
				if err != nil {
					log.Println(err.Error())
				} else {
					go post("http://127.0.0.1:80/pub/"+action.Target, string(js))
					log.Println("Task resend to queue", action)
				}
			}
		}
	}
}

func checkAlive() {
	zonds, _ := client.SMembers("Zond-online").Result()
	if len(zonds) > 0 {
		// log.Println("active", zonds)
		for _, zond := range zonds {
			tp, _ := client.Get(zond + "/alive").Result()
			// log.Println(zond, tp)
			if tp == "" {
				u, _ := uuid.NewV4()
				var Uuid = u.String()
				var msec = time.Now().Unix()
				action := Action{Action: "alive", Uuid: Uuid, Created: msec}
				js, _ := json.Marshal(action)
				client.Set(zond+"/alive", Uuid, 90*time.Second)
				go post("http://127.0.0.1:80/pub/zond:"+zond, string(js))
			} else {
				log.Println(zond, "— removed")
				client.SRem("Zond-online", zond)

				client.HDel("zond:city", zond)
				client.HDel("zond:country", zond)
				client.HDel("zond:asn", zond)
				go delete("http://127.0.0.1:80/pub/zond:" + zond)
				getActiveDestinations()
			}
		}
	}
}

func ZondPong(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)

		if err != nil {
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
		} else {
			var t Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println("pong from", t.ZondUuid, r.Header.Get("X-Forwarded-For"))
			tp, _ := client.Get(t.ZondUuid + "/alive").Result()
			if t.Uuid == tp {
				client.Del(t.ZondUuid + "/alive")
				// log.Print(t.ZondUuid, "Zond pong")
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "ok"}`)
			}
		}
	}
}

func ZondSub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-ZondUuid")
	if len(uuid) > 0 {
		log.Println(uuid, "— connected")
		client.SAdd("Zond-online", uuid)
		usersCount, _ := client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)

		for i := 0; i < 5; i++ {
			var data = r.Header.Get("X-Channel-Id" + fmt.Sprint(i))
			if strings.HasPrefix(data, "City") {
				var city = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HSet("zond:city", uuid, city)
			} else if strings.HasPrefix(data, "Country") {
				var country = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HSet("zond:country", uuid, country)
			} else if strings.HasPrefix(data, "ASN") {
				var asn = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HSet("zond:asn", uuid, asn)
			}
		}
		getActiveDestinations()
	}
}

func ZondUnsub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-ZondUuid")
	if len(uuid) > 0 {
		log.Println(r.Header.Get("X-ZondUuid"), "— disconnected")
		client.SRem("Zond-online", r.Header.Get("X-ZondUuid"))
		usersCount, _ := client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)

		for i := 0; i < 5; i++ {
			var data = r.Header.Get("X-Channel-Id" + fmt.Sprint(i))
			if strings.HasPrefix(data, "City") {
				// var city = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HDel("zond:city", uuid)
			} else if strings.HasPrefix(data, "Country") {
				// var country = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HDel("zond:country", uuid)
			} else if strings.HasPrefix(data, "ASN") {
				// var asn = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				client.HDel("zond:asn", uuid)
			}
		}
		getActiveDestinations()
	}
}

func getActiveDestinations() {
	zonds, _ := client.SMembers("Zond-online").Result()
	// if len(zonds) > 0 {
	// 	// for _, zond := range zonds {
	// }

	cities, _ := client.HVals("zond:city").Result()
	if len(cities) > 0 {
		cities = SliceUniqMap(cities)
	}

	countries, _ := client.HVals("zond:country").Result()
	if len(countries) > 0 {
		countries = SliceUniqMap(countries)
	}

	asns, _ := client.HVals("zond:asn").Result()
	if len(asns) > 0 {
		asns = SliceUniqMap(asns)
	}

	log.Println(zonds, cities, countries, asns)

	channels := Channels{Action: "destinations", Zonds: zonds, Countries: countries, Cities: cities, ASNs: asns}
	js, _ := json.Marshal(channels)
	// log.Println(string(js))

	go post("http://127.0.0.1:80/pub/destinations", string(js))
}

func SliceUniqMap(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	j := 0
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		s[j] = v
		j++
	}
	return s[:j]
}

func post(url string, jsonData string) string {
	var jsonStr = []byte(jsonData)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Println(err)
		return "err"
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

func delete(url string) string {
	req, err := http.NewRequest("DELETE", url, nil)

	client := &http.Client{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Println(err)
		return "err"
	}
	return "ok"
}

func ShowCreateForm(w http.ResponseWriter, r *http.Request, zonduuid string) {
	varmap := map[string]interface{}{
		"ZondUUID":       zonduuid,
		"Version":        version,
		csrf.TemplateTag: csrf.TemplateField(r),
	}

	tmpl, _ := templ.New("dashboard", Asset).Parse("dashboard.html")
	tmpl.Execute(w, varmap)
}

func ShowMyTasks(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	userUuid, _ := client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := client.SCard("user/tasks/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := GetPaginator(int(page), int(count), perPage)

	var results []Action
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = client.SScan("user/tasks/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			for i, val := range keys {
				keys[i] = "task/" + val
			}

			items, _ := client.MGet(keys...).Result()
			for _, val := range items {
				var t Action
				err := json.Unmarshal([]byte(val.(string)), &t)
				if err != nil {
					log.Println(err.Error())
				}
				results = append(results, t)
			}
			// log.Println(len(results), count, results)
		}
		// log.Println(len(results), count, currentPage, cursor, cursorNew, perPage)
	}

	pager := pagination.New(int(count), perPage, currentPage, "/my/tasks")

	varmap := map[string]interface{}{
		"Version":        version,
		"User":           r.Header.Get("X-Forwarded-User"),
		"UserUUID":       userUuid,
		"Results":        results,
		"AllCount":       count,
		"Pages":          pages,
		"Page":           page,
		"HasPrev":        hasPrev,
		"HasNext":        hasNext,
		"pager":          pager,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	// log.Println(varmap)

	// tmpl := template.Must(template.ParseFiles("templates/tasks.html"))
	tmpl, _ := templ.New("tasks", Asset).Parse("tasks.html")
	tmpl.Execute(w, varmap)
}

func ShowRepeatableTasks(w http.ResponseWriter, r *http.Request) {
	userUuid, _ := client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	titles, _ := client.Keys("tasks-repeatable-*").Result()
	count := len(titles)
	// log.Println(count, titles)

	var results []Action
	if count > 0 {
		var keys []string
		var err error
		for _, val := range titles {
			keys, _, err = client.SScan(val, 0, "", 0).Result()
			if err != nil {
				log.Println(err)
			} else {
				// log.Println(keys)
				for _, val := range keys {
					var t Action
					err := json.Unmarshal([]byte(val), &t)
					if err != nil {
						log.Println(err.Error())
					}
					results = append(results, t)
				}
			}
		}
		// log.Println(len(results), count, results)
	}

	varmap := map[string]interface{}{
		"Version":        version,
		"User":           r.Header.Get("X-Forwarded-User"),
		"UserUUID":       userUuid,
		"Results":        results,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	// log.Println(varmap)

	// tmpl := template.Must(template.ParseFiles("templates/tasks.html"))
	tmpl, _ := templ.New("repeatable", Asset).Parse("repeatable.html")
	tmpl.Execute(w, varmap)
}

func ShowMyZonds(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	userUuid, _ := client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := client.SCard("user/zonds/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := GetPaginator(int(page), int(count), perPage)

	var results []Zond
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = client.SScan("user/zonds/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			// log.Println(keys)
			for i, val := range keys {
				keys[i] = "zonds/" + val
			}

			items, _ := client.MGet(keys...).Result()
			for _, val := range items {
				if val != nil {
					var t Zond
					err := json.Unmarshal([]byte(val.(string)), &t)
					if err != nil {
						log.Println(err.Error())
					}
					results = append(results, t)
				}
			}
			log.Println(len(results), count, results)
		}
		// log.Println(len(results), count, currentPage, cursor, cursorNew, perPage)
	}

	pager := pagination.New(int(count), perPage, currentPage, "/my/zonds")

	varmap := map[string]interface{}{
		"Version":        version,
		"User":           r.Header.Get("X-Forwarded-User"),
		"UserUUID":       userUuid,
		"Results":        results,
		"AllCount":       count,
		"Pages":          pages,
		"Page":           page,
		"HasPrev":        hasPrev,
		"HasNext":        hasNext,
		"pager":          pager,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	// log.Println(varmap)

	tmpl, _ := templ.New("zonds", Asset).Parse("zonds.html")
	tmpl.Execute(w, varmap)
}

func GetPaginator(page int, total_count int, per_page int) (currentPage int, pages int, hasPrev bool, hasNext bool) {
	pages = int(math.Ceil(float64(total_count) / float64(per_page)))
	if page > pages {
		page = pages
	} else if page < 1 {
		page = 1
	}

	hasPrev = page > 1
	hasNext = page < pages

	currentPage = page

	return
}

func ShowVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, version)
}

func UserAuthHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, version)
}

func UserLoginHandler(w http.ResponseWriter, r *http.Request) {
	var errorMessage = ""

	if r.Method == "POST" {
		login := r.PostFormValue("login")
		login = strings.TrimSpace(login)
		login = strings.ToLower(login)
		if !mail.ValidateEmail(login) {
			login = ""
		}
		password := r.PostFormValue("password")
		password = strings.TrimSpace(password)

		// var errorMessage = false

		// nothing fancy here, it is just a demo so every user has the same password
		// and if it doesn't match render the login page and present user with error message
		if login == "" || password == "" {
			errorMessage = "no login/pass provided"
			// var redirectURL = r.URL.Host + "/login"
			// http.Redirect(w, r, redirectURL, http.StatusFound)
		} else {
			hash, _ := client.Get("user/pass/" + login).Result()

			res := utils.CheckPasswordHash(password, hash)
			if !res {
				errorMessage = "password incorrect"
				// var redirectURL = r.URL.Host + "/login"
				// http.Redirect(w, r, redirectURL, http.StatusFound)
			} else {
				var s = securecookie.New(nsCookieHashKey, nil)
				value := map[string]string{
					"user": login,
				}

				// encode username to secure cookie
				if encoded, err := s.Encode(nsCookieName, value); err == nil {
					cookie := &http.Cookie{
						Name:    nsCookieName,
						Value:   encoded,
						Domain:  r.URL.Host,
						Expires: time.Now().AddDate(1, 0, 0),
						Path:    "/",
					}
					http.SetCookie(w, cookie)
				}

				// after successful login redirect to original destination (if it exists)
				var redirectURL = r.URL.Host + "/user"
				if cookie, err := r.Cookie(nsRedirectCookieName); err == nil {
					redirectURL = cookie.Value
				}
				// ... and delete the original destination holder cookie
				http.SetCookie(w, &http.Cookie{
					Name:    nsRedirectCookieName,
					Value:   "deleted",
					Domain:  r.URL.Host,
					Expires: time.Now().Add(time.Hour * -24),
					Path:    "/",
				})

				http.Redirect(w, r, redirectURL, http.StatusFound)
				return
			}
		}
	}

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("login", Asset).Parse("login.html")
	tmpl.Execute(w, varmap)
}

func userRegisterHandler(w http.ResponseWriter, r *http.Request) {
	var errorMessage = ""

	if r.Method == "POST" {
		login := r.PostFormValue("email")
		login = mail.Normalize(login)
		if !mail.Validate(login) {
			login = ""
		}

		if login == "" {
			errorMessage = "wrong email"
			// var redirectURL = r.URL.Host + "/register"
			// http.Redirect(w, r, redirectURL, http.StatusFound)
		} else {
			hash, _ := client.Get("user/pass/" + login).Result()
			if hash != "" {
				errorMessage = "already registered"
				// var redirectURL = r.URL.Host + "/login"
				// http.Redirect(w, r, redirectURL, http.StatusFound)
			} else {
				password := utils.RandStr(12)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				client.Set("user/pass/"+login, hash, 0)

				u, _ := uuid.NewV4()
				var Uuid = u.String()
				client.Set("user/uuid/"+login, Uuid, 0)

				go mail.SendMail(login, "Your password", "password: "+password, fqdn)

				var s = securecookie.New(nsCookieHashKey, nil)
				value := map[string]string{
					"user": login,
				}

				// encode username to secure cookie
				if encoded, err := s.Encode(nsCookieName, value); err == nil {
					client.Set("user/session/"+encoded, login, 0)
					cookie := &http.Cookie{
						Name:    nsCookieName,
						Value:   encoded,
						Domain:  r.URL.Host,
						Expires: time.Now().AddDate(1, 0, 0),
						Path:    "/",
					}
					http.SetCookie(w, cookie)
				}

				// after successful login redirect to original destination (if it exists)
				var redirectURL = r.URL.Host + "/user"
				if cookie, err := r.Cookie(nsRedirectCookieName); err == nil {
					redirectURL = cookie.Value
				}
				// ... and delete the original destination holder cookie
				http.SetCookie(w, &http.Cookie{
					Name:    nsRedirectCookieName,
					Value:   "deleted",
					Domain:  r.URL.Host,
					Expires: time.Now().Add(time.Hour * -24),
					Path:    "/",
				})

				http.Redirect(w, r, redirectURL, http.StatusFound)
				return
			}
		}
	}

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("register", Asset).Parse("register.html")
	tmpl.Execute(w, varmap)
}

func userRecoverHandler(w http.ResponseWriter, r *http.Request) {
	var errorMessage = ""

	if r.Method == "POST" {
		login := r.PostFormValue("email")
		login = mail.Normalize(login)
		if !mail.Validate(login) {
			login = ""
		}

		if login == "" {
			errorMessage = "wrong email"
			// var redirectURL = r.URL.Host + "/register"
			// http.Redirect(w, r, redirectURL, http.StatusFound)
		} else {
			hash, _ := client.Get("user/pass/" + login).Result()
			if hash == "" {
				errorMessage = "user not found"
			} else {
				password := utils.RandStr(32)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				client.Set("user/recover/"+login, hash, 0)

				go mail.SendMail(login, "Reset password", `<a href="http://`+fqdn+`/reset?hash=`+password+`&email=`+login+`">Click to reset</a>`, fqdn)

				errorMessage = "Email with recovery link sent"
			}
		}
	}

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("password_recovery", Asset).Parse("password_recovery.html")
	tmpl.Execute(w, varmap)
}

func userResetHandler(w http.ResponseWriter, r *http.Request) {
	var redirectURL = r.URL.Host + "/user"

	password := r.FormValue("hash")
	password = strings.Replace(password, " ", "+", -1)
	login := r.FormValue("email")
	login = mail.Normalize(login)
	if !mail.Validate(login) {
		login = ""
	}

	if login != "" {
		hash, _ := client.Get("user/recover/" + login).Result()
		if hash != "" {
			res := utils.CheckPasswordHash(password, hash)
			if !res {
				log.Println("hash mistmatched", password, login)
			}
			if res {
				password = utils.RandStr(12)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				client.Set("user/pass/"+login, hash, 0)

				go mail.SendMail(login, "Your new password", "password: "+password, fqdn)

				var s = securecookie.New(nsCookieHashKey, nil)
				value := map[string]string{
					"user": login,
				}

				// encode username to secure cookie
				if encoded, err := s.Encode(nsCookieName, value); err == nil {
					client.Set("user/session/"+encoded, login, 0)
					cookie := &http.Cookie{
						Name:    nsCookieName,
						Value:   encoded,
						Domain:  r.URL.Host,
						Expires: time.Now().AddDate(1, 0, 0),
						Path:    "/",
					}
					http.SetCookie(w, cookie)
				}

				// ... and delete the original destination holder cookie
				http.SetCookie(w, &http.Cookie{
					Name:    nsRedirectCookieName,
					Value:   "deleted",
					Domain:  r.URL.Host,
					Expires: time.Now().Add(time.Hour * -24),
					Path:    "/",
				})
			}
		}
	}

	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func userInfoHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `%[1]s at %[2]s (%[3]s)`, r.Header.Get("X-Forwarded-User"), fqdn, version)
}

func authHandler(w http.ResponseWriter, r *http.Request) {
	var s = securecookie.New(nsCookieHashKey, nil)
	// get the cookie from the request
	if cookie, err := r.Cookie(nsCookieName); err == nil {
		value := make(map[string]string)
		// try to decode it
		if err = s.Decode(nsCookieName, cookie.Value, &value); err == nil {
			// user, _ := client.Get("user/session/"+value["user"]).Result()

			// if if succeeds set X-Forwarded-User header and return HTTP 200 status code
			w.Header().Add("X-Forwarded-User", value["user"])
			w.WriteHeader(http.StatusOK)
			return
		}
	}

	// otherwise return HTTP 401 status code
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}
