package handlers

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ad/gocc/bindata"
	"github.com/ad/gocc/ccredis"
	"github.com/ad/gocc/structs"
	"github.com/ad/gocc/utils"

	pagination "github.com/AndyEverLie/go-pagination-bootstrap"
	templ "github.com/arschles/go-bindata-html-template"
	"github.com/gorilla/csrf"
	uuid "github.com/nu7hatch/gouuid"
)

var (
	// Regular expression used to validate RFC1035 hostnames*/
	hostnameRegex = regexp.MustCompile(`^(([a-zA-Z]|[a-zA-Z][a-zA-Z0-9\-]*[a-zA-Z0-9])\.)*([A-Za-z]|[A-Za-z][A-Za-z0-9\-]*[A-Za-z0-9])$`)

	// Simple regular expression for IPv4 values, more rigorous checking is done via net.ParseIP
	ipv4Regex = regexp.MustCompile(`^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$`)
)

func ShowRepeatableTasks(w http.ResponseWriter, r *http.Request) {
	userUuid, _ := ccredis.Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		ccredis.Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	titles, _ := ccredis.Client.Keys("tasks-repeatable-*").Result()
	count := len(titles)
	// log.Println(count, titles)

	var results []structs.Action
	if count > 0 {
		var keys []string
		var err error
		for _, val := range titles {
			keys, _, err = ccredis.Client.SScan(val, 0, "", 0).Result()
			if err != nil {
				log.Println(err)
			} else {
				// log.Println(keys)
				for _, val := range keys {
					var t structs.Action
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
		"Version":        Version,
		"User":           r.Header.Get("X-Forwarded-User"),
		"UserUUID":       userUuid,
		"Results":        results,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	// log.Println(varmap)

	// tmpl := template.Must(template.ParseFiles("templates/tasks.html"))
	tmpl, _ := templ.New("repeatable", bindata.Asset).Parse("repeatable.html")
	tmpl.Execute(w, varmap)
}

func TaskRepeatableRemoveHandler(w http.ResponseWriter, r *http.Request) {
	// if r.Method == "POST" {
	uuid := r.FormValue("uuid")

	if len(uuid) != 36 || strings.Count(uuid, "-") != 4 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("Missing required UUID param."))
		return
	}

	titles, _ := ccredis.Client.Keys("tasks-repeatable-*").Result()
	count := len(titles)
	// log.Println(count, titles)

	if count > 0 {
		var keys []string
		var err error
		for _, val := range titles {
			keys, _, err = ccredis.Client.SScan(val, 0, "", 0).Result()
			if err != nil {
				log.Println(err)
			} else {
				// log.Println(keys)
				for _, item := range keys {
					var t structs.Action
					err := json.Unmarshal([]byte(item), &t)
					if err != nil {
						log.Println(err.Error())
					}
					if t.UUID == uuid {
						ccredis.Client.SRem(val, item)
					}
				}
			}
		}
	}
	// }

	// if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
	fmt.Fprintf(w, `{"status": "ok"}`)
	// } else {
	// 	ShowRepeatableTasks(w, r)
	// }
}

func TaskCreateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		ip := r.FormValue("ip")

		if len(ip) == 0 {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"status": "error", "error": "Missing required IP param"}`)
			return
		}

		taskType := r.FormValue("type")
		taskTypes := map[string]bool{
			"ping":       true,
			"head":       true,
			"dns":        true,
			"traceroute": true,
		}

		taskMainType := "task"
		taskMainTypes := map[string]bool{
			"task":        true,
			"measurement": true,
		}
		if taskMainTypes[r.FormValue("maintype")] {
			taskMainType = r.FormValue("maintype")
		}

		taskCount, err := strconv.ParseInt(r.FormValue("taskcount"), 10, 64)
		if err != nil {
			taskCount = 1
		}

		if taskTypes[taskType] {
			if taskType == "head" {
				// check http(s)://hostname
				if strings.HasPrefix(ip, "http://") || strings.HasPrefix(ip, "https://") {
					s := strings.SplitN(ip, "://", 2)
					proto, addr := s[0], s[1]

					if !ipv4Regex.MatchString(addr) && !hostnameRegex.MatchString(addr) {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprintf(w, `{"status": "error", "error": "wrong ip/hostname"}`)
						return
					} else {
						ip = proto + "://" + addr
					}
				} else {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"status": "error", "error": "must start with http(s)://"}`)
					return
				}
			} else if taskType == "dns" {
				// check ip/hostname-resolver
				var resolverAddress = "8.8.8.8"
				if strings.Count(ip, "-") == 1 {
					s := strings.SplitN(ip, "-", 2)
					ip, resolverAddress = s[0], s[1]
				}
				if ipv4Regex.MatchString(ip) || hostnameRegex.MatchString(ip) {
					if resolverAddress == "8.8.8.8" || ipv4Regex.MatchString(resolverAddress) || hostnameRegex.MatchString(resolverAddress) {
						ip = ip + "-" + resolverAddress
					} else {
						w.WriteHeader(http.StatusBadRequest)
						fmt.Fprintf(w, `{"status": "error", "error": "wrong resolver"}`)
						return
					}
				} else {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"status": "error", "error": "wrong ip/hostname"}`)
					return
				}
			} else {
				// check ip/hostname
				if !ipv4Regex.MatchString(ip) && !hostnameRegex.MatchString(ip) {
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprintf(w, `{"status": "error", "error": "wrong ip/hostname"}`)
					return
				}
			}

			dest := r.FormValue("dest")
			destination := "tasks"
			if len(dest) > 4 && strings.Count(dest, ":") == 2 {
				target := strings.Join(strings.Split(dest, ":")[2:], ":")
				if strings.HasPrefix(dest, "zond:uuid:") {
					test, _ := ccredis.Client.SIsMember("Zond-online", target).Result()
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
			u, _ := uuid.NewV4()
			var Uuid = u.String()
			var msec = time.Now().Unix()

			ccredis.Client.SAdd("tasks-new", Uuid)

			// users, _ := client.SMembers("tasks-new").Result()
			// usersCount, _ := client.SCard("tasks-new").Result()
			// log.Println("tasks-new", users, usersCount)

			userUuid, _ := ccredis.Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
			if userUuid == "" {
				u, _ := uuid.NewV4()
				userUuid = u.String()
				ccredis.Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
			}

			action := structs.Action{Action: taskType, Param: ip, UUID: Uuid, Created: msec, Creator: userUuid, Target: destination, Repeat: repeatType, Type: taskMainType, Count: taskCount}
			js, _ := json.Marshal(action)

			ccredis.Client.Set("task/"+Uuid, string(js), 0)
			ccredis.Client.SAdd("user/tasks/"+userUuid, Uuid)
			if repeatType != "single" {
				t := time.Now()
				tnew := t.Add(time.Duration(repeatTypes[repeatType]) * time.Second).Unix()
				t300 := (tnew - (tnew % 300))
				log.Println("next start will be at ", strconv.FormatInt(t300, 10))

				ccredis.Client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300, 10), string(js))
			}

			if taskMainType != "task" {
				go utils.Post("http://127.0.0.1:80/pub/mngrtasks", string(js))
			} else {
				go utils.Post("http://127.0.0.1:80/pub/"+destination, string(js))
			}

			log.Println(ip, taskType, Uuid)
		} else {
			// w.Header().Set("X-CSRF-Token", csrf.Token(r))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"status": "error", "error": "wrong task type"}`)
			return
		}
	}

	if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
		// w.Header().Set("X-CSRF-Token", csrf.Token(r))
		fmt.Fprintf(w, `{"status": "ok"}`)
	} else {
		ShowCreateForm(w, r)
	}

}

func TaskZondBlockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t structs.Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.ZondUUID, "wants to", t.Action, t.UUID)
			zondBusy, err := ccredis.Client.SIsMember("zond-busy", t.ZondUUID).Result()
			if (err != nil) || (zondBusy != true) {
				count := ccredis.Client.SRem("tasks-new", t.UUID)
				if count.Val() == int64(1) {
					ccredis.Client.SAdd("tasks-process", t.ZondUUID+"/"+t.UUID)
					ccredis.Client.SAdd("zond-busy", t.ZondUUID)
					ccredis.Client.Set(t.ZondUUID+"/"+t.UUID+"/processing", "1", 60*time.Second)
					log.Println(t.ZondUUID, `{"status": "ok", "message": "ok"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)
				} else {
					log.Println(t.ZondUUID, `{"status": "error", "message": "task not found"}`)
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

func TaskZondResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t structs.Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.ZondUUID, "wants to", t.Action, t.UUID)
			ccredis.Client.SRem("zond-busy", t.ZondUUID)

			if t.Action == "result" {
				taskProcessing, err := ccredis.Client.SIsMember("tasks-process", t.ZondUUID+"/"+t.UUID).Result()
				if (err != nil) || (taskProcessing != true) {
					log.Println(`{"status": "error", "message": "task not found"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				} else {
					count := ccredis.Client.SRem("tasks-process", t.ZondUUID+"/"+t.UUID)
					if count.Val() == int64(1) {
						ccredis.Client.SAdd("tasks-done", t.ZondUUID+"/"+t.UUID+"/"+t.Result)
						log.Println(t.ZondUUID, `{"status": "ok", "message": "ok"}`)
						// w.Header().Set("X-CSRF-Token", csrf.Token(r))
						fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)

						js, _ := ccredis.Client.Get("task/" + t.UUID).Result()
						// log.Println(js)
						var task structs.Action
						err = json.Unmarshal([]byte(js), &task)
						if err != nil {
							log.Println(err.Error())
						}
						task.Result = t.Result
						task.ZondUUID = t.ZondUUID
						task.Updated = time.Now().Unix()

						jsonBody, err := json.Marshal(task)
						if err != nil {
							http.Error(w, "Error converting results to json",
								http.StatusInternalServerError)
						}
						ccredis.Client.Set("task/"+t.UUID, jsonBody, 0)
						go utils.Post("http://127.0.0.1:80/pub/tasks/done", string(jsonBody))
					} else {
						log.Println(t.ZondUUID, `{"status": "error", "message": "task not found"}`)
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

func TaskMngrBlockHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t structs.Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.MngrUUID, "wants to", t.Action, t.UUID)

			count := ccredis.Client.SRem("tasks-new", t.UUID)
			if count.Val() == int64(1) {
				ccredis.Client.SAdd("tasks-process", t.MngrUUID+"/"+t.UUID)
				ccredis.Client.Set(t.MngrUUID+"/"+t.UUID+"/processing", "1", 300*time.Second) // FIXME: time
				log.Println(t.MngrUUID, `{"status": "ok", "message": "ok"}`)
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)
			} else {
				log.Println(t.MngrUUID, `{"status": "error", "message": "task not found"}`)
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
			}
		}
	} else {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
	}
}

func TaskMngrResultHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Error reading request body",
				http.StatusInternalServerError)
		} else {
			var t structs.Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			log.Println(t.MngrUUID, "wants to", t.Action, t.UUID)
			if t.Action == "result" {
				taskProcessing, err := ccredis.Client.SIsMember("tasks-process", t.MngrUUID+"/"+t.UUID).Result()
				if (err != nil) || (taskProcessing != true) {
					log.Println(`{"status": "error", "message": "task not found"}`)
					// w.Header().Set("X-CSRF-Token", csrf.Token(r))
					fmt.Fprintf(w, `{"status": "error", "message": "task not found"}`)
				} else {
					count := ccredis.Client.SRem("tasks-process", t.MngrUUID+"/"+t.UUID)
					if count.Val() == int64(1) {
						ccredis.Client.SAdd("tasks-done", t.MngrUUID+"/"+t.UUID+"/"+t.Result)
						log.Println(t.MngrUUID, `{"status": "ok", "message": "ok"}`)
						// w.Header().Set("X-CSRF-Token", csrf.Token(r))
						fmt.Fprintf(w, `{"status": "ok", "message": "ok"}`)

						js, _ := ccredis.Client.Get("task/" + t.UUID).Result()
						// log.Println(js)
						var task structs.Action
						err = json.Unmarshal([]byte(js), &task)
						if err != nil {
							log.Println(err.Error())
						}
						task.Result = t.Result
						task.MngrUUID = t.MngrUUID
						task.Updated = time.Now().Unix()

						jsonBody, err := json.Marshal(task)
						if err != nil {
							http.Error(w, "Error converting results to json",
								http.StatusInternalServerError)
						}
						ccredis.Client.Set("task/"+t.UUID, jsonBody, 0)
						go utils.Post("http://127.0.0.1:80/pub/tasks/done", string(jsonBody))
					} else {
						log.Println(t.MngrUUID, `{"status": "error", "message": "task not found"}`)
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

func ShowMyTasks(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	userUuid, _ := ccredis.Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		ccredis.Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := ccredis.Client.SCard("user/tasks/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := utils.GetPaginator(int(page), int(count), perPage)

	var results []structs.Action
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = ccredis.Client.SScan("user/tasks/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			for i, val := range keys {
				keys[i] = "task/" + val
			}

			items, _ := ccredis.Client.MGet(keys...).Result()
			for _, val := range items {
				var t structs.Action
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
		"Version":        Version,
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
	tmpl, _ := templ.New("tasks", bindata.Asset).Parse("tasks.html")
	tmpl.Execute(w, varmap)
}

func ShowCreateForm(w http.ResponseWriter, r *http.Request) {
	varmap := map[string]interface{}{
		// "ZondUUID":       zonduuid,
		"Version":        Version,
		"FQDN":           Fqdn,
		csrf.TemplateTag: csrf.TemplateField(r),
	}

	tmpl, _ := templ.New("dashboard", bindata.Asset).Parse("dashboard.html")
	tmpl.Execute(w, varmap)
}
