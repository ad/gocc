package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/go-redis/redis"
	"github.com/nu7hatch/gouuid"
)

type Action struct {
	ZondUuid string `json:"zond"`
	Action   string `json:"action"`
	Param    string `json:"param"`
	Result   string `json:"result"`
	Uuid     string `json:"uuid"`
}

type Result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

var port = flag.String("port", "9000", "Port to listen on")
var serveruuid, _ = uuid.NewV4()

var client = redis.NewClient(&redis.Options{
	Addr:     "localhost:6379",
	Password: "", // no password set
	DB:       0,  // use default DB
})
var results []string

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

func TaskCreatetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ip := r.FormValue("ip")

		if len(ip) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing required IP param."))
			return
		}

		u, _ := uuid.NewV4()
		var Uuid = u.String()

		client.SAdd("tasks-new", Uuid)

		users, _ := client.SMembers("tasks-new").Result()
		usersCount, _ := client.SCard("tasks-new").Result()
		log.Println("tasks-new", users, usersCount)

		action := Action{Action: "ping", Param: ip, Uuid: Uuid}
		js, _ := json.Marshal(action)

		client.Set("task/"+Uuid, string(js), 0)

		go post("http://127.0.0.1:80/pub/tasks", string(js))

		log.Println(ip, Uuid)
	}
	ShowCreateForm(w, r)
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

			count := client.SRem("tasks-new", t.Uuid)
			if count.Val() == int64(1) {
				client.SAdd("tasks-process", t.ZondUuid+"/"+t.Uuid)
				log.Println(t.ZondUuid, `{"status": "ok", "message": "ok"}`)
				fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)
			} else {
				log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
				fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
			}
		}
		results = append(results, string(body))
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

			if t.Action == "result" {
				taskProcessing, err := client.SIsMember("tasks-process", t.ZondUuid+"/"+t.Uuid).Result()
				if (err != nil) || (taskProcessing != true) {
					log.Println(`{"status": "error", "message": "task not found"}`)
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				} else {
					count := client.SRem("tasks-process", t.ZondUuid+"/"+t.Uuid)
					if count.Val() == int64(1) {
						client.SAdd("tasks-done", t.ZondUuid+"/"+t.Uuid+"/"+t.Result)
						log.Println(t.ZondUuid, `{"status": "ok", "message": "ok"}`)
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

						jsonBody, err := json.Marshal(task)
						if err != nil {
							http.Error(w, "Error converting results to json",
								http.StatusInternalServerError)
						}
						client.Set("task/"+t.Uuid, jsonBody, 0)
					} else {
						log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
						fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
					}
				}
			}
		}
		results = append(results, string(body))
	} else {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	flag.Parse()
}

func main() {
	go resendOffline()
	// _ = client.Set("Zond-counter", 0, 0).Err()

	results = append(results, time.Now().Format(time.RFC3339))

	mux := http.NewServeMux()
	mux.HandleFunc("/", GetHandler)
	mux.HandleFunc("/sub", ZondSub)
	mux.HandleFunc("/unsub", ZondUnsub)
	mux.HandleFunc("/task/create", TaskCreatetHandler)
	mux.HandleFunc("/task/block", TaskBlockHandler)
	mux.HandleFunc("/task/result", TaskResultHandler)

	log.Printf("listening on port %s", *port)
	log.Fatal(http.ListenAndServe(":"+*port, mux))
}

func resendOffline() {
	tasks, _ := client.SMembers("tasks-new").Result()
	log.Println("active tasks", tasks)

	if len(tasks) > 0 {
		for _, task := range tasks {
			js, _ := client.Get("task/" + task).Result()
			go post("http://127.0.0.1:80/pub/tasks", string(js))
			log.Println(js)
		}
	}
}

func ZondSub(w http.ResponseWriter, r *http.Request) {
	log.Print(r.Header.Get("X-ZondUuid"), "ZondSub done")

	if len(r.Header.Get("X-ZondUuid")) > 0 {
		client.SAdd("Zond-online", r.Header.Get("X-ZondUuid"))
		usersCount, _ := client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)
	}
}

func ZondUnsub(w http.ResponseWriter, r *http.Request) {
	log.Print(r.Header.Get("X-ZondUuid"), "ZondUnsub done")

	if len(r.Header.Get("X-ZondUuid")) > 0 {
		client.SRem("Zond-online", r.Header.Get("X-ZondUuid"))
		usersCount, _ := client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)
	}
}

func post(url string, jsonData string) string {
	var jsonStr = []byte(jsonData)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

func ShowCreateForm(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `
<html>
<head>
    <title>Control center</title>
</head>
<body>
    <div>
        <form method="POST" action="/task/create">
            <input type="text" name="ip" id="ip" value="127.0.0.1" placeholder="IP">
            <input type="submit" value="Do it!">
        </form>
    </div>
<hr>
</body>
</html>`)
}
