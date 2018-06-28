package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"syscall"

	"github.com/kardianos/osext"

	"github.com/blang/semver"
	"github.com/go-redis/redis"
	"github.com/nu7hatch/gouuid"
	"github.com/rhysd/go-github-selfupdate/selfupdate"
)

const version = "0.0.16"

func selfUpdate(slug string) error {
	previous := semver.MustParse(version)
	latest, err := selfupdate.UpdateSelf(previous, slug)
	if err != nil {
		return err
	}

	if previous.Equals(latest.Version) {
		// fmt.Println("Current binary is the latest version", version)
	} else {
		fmt.Println("Update successfully done to version", latest.Version)
		fmt.Println("Release note:\n", latest.ReleaseNotes)
		file, err := osext.Executable()
		if err != nil {
			return err
		}
		err = syscall.Exec(file, os.Args, os.Environ())
		if err != nil {
			log.Fatal(err)
		}
	}

	return nil
}

type Action struct {
	ZondUuid   string `json:"zond"`
	Action     string `json:"action"`
	Param      string `json:"param"`
	Result     string `json:"result"`
	Uuid       string `json:"uuid"`
	ParentUuid string `json:"parent"`
	Created    int64  `json:"created"`
	Updated    int64  `json:"updated"`
}

type Result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}

type Zond struct {
	Uuid    string `json:"uuid"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Updated int64  `json:"updated"`
}

// Geodata struct
type Geodata struct {
	City                         string  `json:"city"`
	Country                      string  `json:"country"`
	CountryCode                  string  `json:"country_code"`
	Longitude                    float64 `json:"lon"`
	Latitude                     float64 `json:"lat"`
	AutonomousSystemNumber       uint    `json:"asn"`
	AutonomousSystemOrganization string  `json:"provider"`
}

var port = flag.String("port", "9000", "Port to listen on")
var serveruuid, _ = uuid.NewV4()

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
		var add = IPToWSChannels(ip)
		log.Println("/internal/sub/tasks,zond:" + uuid + "," + add)
		w.Header().Add("X-Accel-Redirect", "/internal/sub/tasks,zond:"+uuid+","+add)
		w.Header().Add("X-Accel-Buffering", "no")
	} else {
		// log.Println("/internal/sub/tasks/done," + ip)

		w.Header().Add("X-Accel-Redirect", "/internal/sub/tasks/done,"+ip)
		w.Header().Add("X-Accel-Buffering", "no")
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(""))
}

func IPToWSChannels(ip string) string {
	url := "http://localhost:9001/?ip=" + ip

	spaceClient := http.Client{
		Timeout: time.Second * 5, // Maximum of 2 secs
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Println(err)
	} else {
		res, getErr := spaceClient.Do(req)
		if getErr != nil {
			log.Println(getErr)
		} else {

			body, readErr := ioutil.ReadAll(res.Body)
			if readErr != nil {
				log.Println(readErr)
			} else {

				geodata := Geodata{}
				jsonErr := json.Unmarshal(body, &geodata)
				if jsonErr != nil {
					log.Println(jsonErr)
				} else {
					var result []string
					result = append(result, "IP:"+ip)

					if geodata.City != "" {
						result = append(result, "City:"+geodata.City)
					}
					if geodata.Country != "" {
						result = append(result, "Country:"+geodata.Country)
					}
					if geodata.AutonomousSystemNumber != 0 {
						result = append(result, "ASN:"+fmt.Sprint(geodata.AutonomousSystemNumber))
					}
					return strings.Join(result[:], ",")
				}
			}
		}
	}
	return ""
}

func ZondCreatetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		name := r.FormValue("name")

		u, _ := uuid.NewV4()
		var Uuid = u.String()
		var msec = time.Now().UnixNano() / 1000000

		if len(name) == 0 {
			name = Uuid
		}

		client.SAdd("zonds", Uuid)

		zond := Zond{Uuid: Uuid, Name: name, Created: msec}
		js, _ := json.Marshal(zond)

		client.Set("zonds/"+Uuid, string(js), 0)

		log.Println("Zond created", Uuid)

		if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
			fmt.Fprintf(w, `{"status": "ok", "uuid": "%s"}`, Uuid)
		} else {
			ShowCreateForm(w, r, Uuid)
		}
	} else {
		ShowCreateForm(w, r, "")
	}
}

func TaskCreatetHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ip := r.FormValue("ip")

		if len(ip) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Missing required IP param."))
			return
		}

		taskType := r.FormValue("type")
		taskTypes := map[string]bool{
			"ping": true,
			"head": true,
		}

		if taskTypes[taskType] {
			u, _ := uuid.NewV4()
			var Uuid = u.String()
			var msec = time.Now().UnixNano() / 1000000

			client.SAdd("tasks-new", Uuid)

			users, _ := client.SMembers("tasks-new").Result()
			usersCount, _ := client.SCard("tasks-new").Result()
			log.Println("tasks-new", users, usersCount)

			action := Action{Action: taskType, Param: ip, Uuid: Uuid, Created: msec}
			js, _ := json.Marshal(action)

			client.Set("task/"+Uuid, string(js), 0)

			go post("http://127.0.0.1:80/pub/tasks", string(js))

			log.Println(ip, taskType, Uuid)
		}
	}

	if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
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
					fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)
				} else {
					log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				}
			} else {
				log.Println(`{"status": "error", "message": "only one task at time is allowed"}`)
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
						task.Updated = time.Now().UnixNano() / 1000000

						jsonBody, err := json.Marshal(task)
						if err != nil {
							http.Error(w, "Error converting results to json",
								http.StatusInternalServerError)
						}
						client.Set("task/"+t.Uuid, jsonBody, 0)
						go post("http://127.0.0.1:80/pub/tasks/done", string(jsonBody))
					} else {
						log.Println(t.ZondUuid, `{"status": "error", "message": "task not found"}`)
						fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
					}
				}
			}
		}
	} else {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func init() {
	log.SetFlags(log.Lmicroseconds | log.Lshortfile)
	flag.Parse()
}

func main() {
	log.Printf("Started version %s", version)

	selfUpdateTicker := time.NewTicker(10 * time.Minute)
	go func(selfUpdateTicker *time.Ticker) {
		for {
			select {
			case <-selfUpdateTicker.C:
				if err := selfUpdate("ad/gocc"); err != nil {
					fmt.Fprintln(os.Stderr, err)
					// os.Exit(1)
				}
			}
		}
	}(selfUpdateTicker)

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

	go resendOffline()

	mux := http.NewServeMux()
	mux.HandleFunc("/", GetHandler)
	mux.HandleFunc("/dispatch/", DispatchHandler)
	mux.HandleFunc("/version", ShowVersion)
	mux.HandleFunc("/task/create", TaskCreatetHandler)
	mux.HandleFunc("/task/block", TaskBlockHandler)
	mux.HandleFunc("/task/result", TaskResultHandler)
	mux.HandleFunc("/zond/pong", ZondPong)
	mux.HandleFunc("/zond/create", ZondCreatetHandler)
	mux.HandleFunc("/zond/sub", ZondSub)
	mux.HandleFunc("/zond/unsub", ZondUnsub)

	log.Printf("listening on port %s", *port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+*port, mux))
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
				go post("http://127.0.0.1:80/pub/tasks", string(js))
				log.Println("Task resend to queue", tp, js)
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
				var msec = time.Now().UnixNano() / 1000000
				action := Action{Action: "alive", Uuid: Uuid, Created: msec}
				js, _ := json.Marshal(action)
				client.Set(zond+"/alive", Uuid, 90*time.Second)
				go post("http://127.0.0.1:80/pub/zond"+zond, string(js))
			} else {
				log.Println(zond, "— removed")
				client.SRem("Zond-online", zond)
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
			tp, _ := client.Get(t.ZondUuid + "/alive").Result()
			if t.Uuid == tp {
				client.Del(t.ZondUuid + "/alive")
				// log.Print(t.ZondUuid, "Zond pong")
				fmt.Fprintf(w, `{"status": "ok"}`)
			}
		}
	}
}

func ZondSub(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Get("X-ZondUuid")) > 0 {
		log.Println(r.Header.Get("X-ZondUuid"), "— connected")
		client.SAdd("Zond-online", r.Header.Get("X-ZondUuid"))
		usersCount, _ := client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)
	}
}

func ZondUnsub(w http.ResponseWriter, r *http.Request) {
	if len(r.Header.Get("X-ZondUuid")) > 0 {
		log.Println(r.Header.Get("X-ZondUuid"), "— disconnected")
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
		log.Println(err)
		return "err"
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

func ShowCreateForm(w http.ResponseWriter, r *http.Request, zonduuid string) {
	fmt.Fprintf(w, `<html>
<head>
    <title>Control center</title>
    <script>
		var socket = new WebSocket("ws://" + location.host + "/sub/tasks/done");

		socket.onmessage = function(message) {
			var event = JSON.parse(message.data);
			console.log(event);

			var table = document.getElementById("commands");

		    var row = table.insertRow(1);

		    var cell1 = row.insertCell(0);
		    var cell2 = row.insertCell(1);
		    var cell3 = row.insertCell(2);
		    var cell4 = row.insertCell(3);

		    var dt = new Date(event.updated).toLocaleString()
		    cell1.innerHTML = dt;
		    cell2.innerHTML = event.zond;
		    cell3.innerHTML = '<span class="action">' + event.action + '</span> <span class="param">' + event.param + '</span>';
		    cell4.innerHTML = event.result;

		    cell3.onclick = function () {
            	createTask(this.querySelector('.action').innerHTML, this.querySelector('.param').innerHTML);
        	}
		};

		function createTask(taskType, taskIp) {
		    var xhr = new XMLHttpRequest();

			xhr.open('POST', '/task/create');
			xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');
			xhr.setRequestHeader('X-Requested-With', 'xmlhttprequest');
			xhr.onload = function() {
			    if (xhr.status !== 200) {
			        alert('Request failed.  Returned status of ' + xhr.status);
			    }
			};
			xhr.send(encodeURI('type='+taskType+'&ip='+taskIp));

			return false;
		}

		function createZond(zondName) {
		    var xhr = new XMLHttpRequest();

			xhr.open('POST', '/zond/create');
			xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');
			xhr.setRequestHeader('X-Requested-With', 'xmlhttprequest');
			xhr.onload = function() {
			    if (xhr.status !== 200) {
			        alert('Request failed.  Returned status of ' + xhr.status);
			    } else {
			    	data = JSON.parse(xhr.responseText);
			    	if (data.status == "ok") {
			    		document.querySelector('#zondUuid').innerText = data.uuid;
			    	} else {
			    		document.querySelector('#zondUuid').innerText = data.message;
			    	}
			    }
			};
			xhr.send(encodeURI('name='+zondName));

			return false;
		}
    </script>
    <style>
		body {
		  font-family: 'Open Sans', sans-serif;
		}
		table {
		    border-collapse: collapse;
		    width: 100%;
		}

		table, th, td {
		    border: 0;
		}
	    th, td {
	    	border-bottom: 1px solid #ddd;
	    	text-align: left;
	    	vertical-align: top;
		    padding: 15px;
		    text-align: left;
		}
		tr:nth-child(even) {
			background-color: #f2f2f2;
		}
		th {
		    height: 50px;
		}
	</style>
</head>
<body>
    <div style="float: left;">
        <form method="POST" action="/task/create" onSubmit="return createTask(document.getElementById('type').value, document.getElementById('ip').value)">
        	<select name="type" id="type">
        		<option value="ping">PING</option>
        		<option value="head">HEAD</option>
        	</select>
            <input type="text" name="ip" id="ip" value="127.0.0.1" placeholder="IP">
            <input type="submit" value="Do it!">
        </form>
    </div>
    <div style="float: right;">
        <form method="POST" action="/zond/create" onSubmit="return createZond(document.getElementById('name').value)">
            <input type="text" name="name" id="name" value="" placeholder="Zond name">
            <input type="submit" value="Add Zond"> <span id="zondUuid">%[1]s</span>
        </form>
    </div>

	<hr style="clear: both;">

    <table border="0" id="commands">
        <tr>
        	<th>Date</th>
        	<th>Executor</th>
            <th>Command</th>
            <th>Results</th>
        </tr>
    </table>
    <div style="position: fixed; bottom: 0; right: 0; padding: 5px; font: 9px sans-serif;">
    	%[2]s
    </div>
</body>
</html>`, zonduuid, version)
}

func ShowVersion(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, version)
}
