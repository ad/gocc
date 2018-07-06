package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"../ccredis"
	"../structs"

	"github.com/ad/gocc/utils"
	uuid "github.com/nu7hatch/gouuid"
)

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

			action := structs.Action{Action: taskType, Param: ip, Uuid: Uuid, Created: msec, Creator: userUuid, Target: destination, Repeat: repeatType}
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

			go utils.Post("http://127.0.0.1:80/pub/"+destination, string(js))

			log.Println(ip, taskType, Uuid)
		} else {
			// w.Header().Set("X-CSRF-Token", csrf.Token(r))
			fmt.Fprintf(w, `{"status": "error", "error": "wrong task type"}`)
			return
		}
	}

	fmt.Fprintf(w, `{"status": "ok"}`)
}
