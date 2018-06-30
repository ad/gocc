package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/ad/gocc/mail"
	"github.com/ad/gocc/selfupdate"
	"github.com/ad/gocc/utils"
	"github.com/go-redis/redis"
	"github.com/gorilla/securecookie"
	"github.com/nu7hatch/gouuid"
)

const version = "0.1.6"

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

type Channels struct {
	Action    string   `json:"action"`
	Zonds     []string `json:"zonds"`
	Countries []string `json:"countries"`
	Cities    []string `json:"cities"`
	ASNs      []string `json:"asns"`
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

	go resendOffline()

	mux := http.NewServeMux()
	mux.HandleFunc("/", GetHandler)
	mux.HandleFunc("/dispatch/", DispatchHandler)
	mux.HandleFunc("/version", ShowVersion)
	mux.HandleFunc("/task/create", TaskCreatetHandler)
	mux.HandleFunc("/zond/task/block", TaskBlockHandler)
	mux.HandleFunc("/zond/task/result", TaskResultHandler)
	mux.HandleFunc("/zond/pong", ZondPong)
	mux.HandleFunc("/zond/create", ZondCreatetHandler)
	mux.HandleFunc("/zond/sub", ZondSub)
	mux.HandleFunc("/zond/unsub", ZondUnsub)
	mux.HandleFunc("/user", userInfoHandler)
	mux.HandleFunc("/user/auth", UserAuthHandler)
	mux.HandleFunc("/recover", userRecoverHandler)
	mux.HandleFunc("/reset", userResetHandler)
	mux.HandleFunc("/login", UserLoginHandler)
	mux.HandleFunc("/register", userRegisterHandler)
	mux.HandleFunc("/auth", authHandler)

	log.Printf("listening on port %s", *port)
	log.Fatal(http.ListenAndServe("127.0.0.1:"+*port, mux))
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

		if taskTypes[taskType] {
			u, _ := uuid.NewV4()
			var Uuid = u.String()
			var msec = time.Now().UnixNano() / 1000000

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

			action := Action{Action: taskType, Param: ip, Uuid: Uuid, Created: msec, Creator: userUuid}
			js, _ := json.Marshal(action)

			client.Set("task/"+Uuid, string(js), 0)

			go post("http://127.0.0.1:80/pub/"+destination, string(js))

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

				// TODO: respect destination

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
				go post("http://127.0.0.1:80/pub/zond:"+zond, string(js))
			} else {
				log.Println(zond, "— removed")
				client.SRem("Zond-online", zond)

				client.HDel("zond:city", zond)
				client.HDel("zond:country", zond)
				client.HDel("zond:asn", zond)
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

			if (event.action == "destinations") {
				addOptions("zonds", "zond:uuid:", event.zonds)
				addOptions("asns", "zond:asn:", event.asns)
				addOptions("countries", "zond:country:", event.countries)
				addOptions("cities", "zond:city:", event.cities)
			} else {
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
			}
		};

		function addOptions(destID, prefix, items) {
			document.getElementById(destID).innerHTML = '';
			for (i = 0; i < items.length; ++i) {
				opt = document.createElement('OPTION');
				opt.textContent = items[i];
				opt.value = prefix+items[i];
				document.getElementById(destID).appendChild(opt);
			}
		}

		function createTask(dest, taskType, taskIp) {
		    var xhr = new XMLHttpRequest();

			xhr.open('POST', '/task/create');
			xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded');
			xhr.setRequestHeader('X-Requested-With', 'xmlhttprequest');
			xhr.onload = function() {
			    if (xhr.status !== 200) {
			        alert('Request failed.  Returned status of ' + xhr.status);
			    }
			};
			xhr.send(encodeURI('dest='+dest+'&type='+taskType+'&ip='+taskIp));

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
		    width: 100%%;
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
		<form method="POST" action="/task/create" onSubmit="return createTask(document.getElementById('destination').value, document.getElementById('type').value, document.getElementById('ip').value)">
			<select name="destination" id="destination">
				<optgroup label="Выберите цель" id=""><option>Любой зонд</option></optgroup>
				<optgroup label="Страны" id="countries"></optgroup>
				<optgroup label="Города" id="cities"></optgroup>
				<optgroup label="ASN" id="asns"></optgroup>
				<optgroup label="Зонды" id="zonds"></optgroup>
			</select>
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

	fmt.Fprintf(w, `<html>
<head>
    <title>Control center login</title>
    <style>
		body {
		  font-family: 'Open Sans', sans-serif;
		}
		table {
		    border-collapse: collapse;
		    width: 100%%;
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
		<form method="POST" action="/login"">
			<input type="text" name="login" placeholder="email"><br>
			<input type="password" name="password" placeholder="password"><br>
            <input type="submit" value="Sign in"><br>
			<span style="color: red">%[1]s</span>
		</form>
		<br>
		<a href="/register">Register</a>
    </div>
</body>
</html>`, errorMessage)
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

	fmt.Fprintf(w, `<html>
<head>
    <title>Control center register</title>
    <style>
		body {
		  font-family: 'Open Sans', sans-serif;
		}
		table {
		    border-collapse: collapse;
		    width: 100%%;
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
		<form method="POST" action="/register"">
			<input type="text" name="email" placeholder="email"><br>
			<input type="submit" value="Register"><br>
			<span style="color: red">%[1]s</span>
        </form>
		<br>
		<a href="/login">Login</a> | <a href="/recover">Recover</a> | 
    </div>
</body>
</html>`, errorMessage)
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

	fmt.Fprintf(w, `<html>
<head>
    <title>Control center password recovery</title>
    <style>
		body {
		  font-family: 'Open Sans', sans-serif;
		}
		table {
		    border-collapse: collapse;
		    width: 100%%;
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
		<form method="POST" action="/recover"">
			<input type="text" name="email" placeholder="email"><br>
			<input type="submit" value="Recover"><br>
			<span style="color: red">%[1]s</span>
        </form>
		<br>
		<a href="/login">Login</a> | <a href="/register">Register</a>
    </div>
</body>
</html>`, errorMessage)
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

				go mail.SendMail(login, "Your newpassword", "password: "+password, fqdn)

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
