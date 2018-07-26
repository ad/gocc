package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	uuid "github.com/nu7hatch/gouuid"
)

func ApiTaskCreateHandler(w http.ResponseWriter, r *http.Request) {
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
				test, _ := Client.SIsMember("Zond-online", target).Result()
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
		var UUID = u.String()
		var msec = time.Now().Unix()

		Client.SAdd("tasks-new", UUID)

		// users, _ := client.SMembers("tasks-new").Result()
		// usersCount, _ := client.SCard("tasks-new").Result()
		// log.Println("tasks-new", users, usersCount)

		userUUID, _ := Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
		if userUUID == "" {
			u, _ := uuid.NewV4()
			userUUID = u.String()
			Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUUID, 0)
		}

		action := Action{Action: taskType, Param: ip, UUID: UUID, Created: msec, Creator: userUUID, Target: destination, Repeat: repeatType, Type: taskMainType, Count: taskCount, TimeOut: 60}
		js, _ := json.Marshal(action)

		Client.Set("task/"+UUID, string(js), 0)
		Client.SAdd("user/tasks/"+userUUID, UUID)
		if repeatType != "single" {
			t := time.Now()
			tnew := t.Add(time.Duration(repeatTypes[repeatType]) * time.Second).Unix()
			t300 := (tnew - (tnew % 300))
			log.Println("next start will be at ", strconv.FormatInt(t300, 10))

			Client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300, 10), string(js))
		}

		if taskMainType != "task" {
			go Post("http://127.0.0.1:80/pub/mngrtasks", string(js))
		} else {
			go Post("http://127.0.0.1:80/pub/"+destination, string(js))
		}
		log.Println(ip, taskType, UUID)
	} else {
		// w.Header().Set("X-CSRF-Token", csrf.Token(r))
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"status": "error", "error": "wrong task type"}`)
		return
	}

	fmt.Fprintf(w, `{"status": "ok"}`)
}
