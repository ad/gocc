package main

import (
	"encoding/json"
	"flag"
	"log"
	"strconv"
	"strings"
	"time"

	uuid "github.com/nu7hatch/gouuid"
)

var HistoryHours = flag.Int64("historyHours", 6, "How many hours check from history of repeatable tasks in case of downtime")

func ResetProcessing() {
	tasks, _ := Client.SMembers("tasks-process").Result()
	if len(tasks) > 0 {
		for _, task := range tasks {
			s := strings.Split(task, "/")
			ZondUuid, taskUuid := s[0], s[1]

			tp, _ := Client.Get(task + "/processing").Result()
			if tp != "1" {
				log.Println("Removed outdated task", tp, ZondUuid, taskUuid)
				Client.SRem("zond-busy", ZondUuid)
				Client.SRem("tasks-process", task)

				Client.SAdd("tasks-new", taskUuid)

				js, _ := Client.Get("task/" + taskUuid).Result()
				var action Action
				err := json.Unmarshal([]byte(js), &action)
				if err != nil {
					log.Println(err.Error())
				} else {
					go Post("http://127.0.0.1:80/pub/"+action.Target, string(js))
					log.Println("Task resend to queue", action)
				}
			}
		}
	}
}

func ResendOffline() {
	tasks, _ := Client.SMembers("tasks-new").Result()

	if len(tasks) > 0 {
		log.Println("active tasks", tasks)
		for _, task := range tasks {
			js, _ := Client.Get("task/" + task).Result()

			var action Action
			err := json.Unmarshal([]byte(js), &action)
			if err != nil {
				log.Println(err.Error())
			} else {
				if action.Result != "" {
					Client.SRem("tasks-new", action.UUID)
				} else {
					go Post("http://127.0.0.1:80/pub/"+action.Target, string(js))
					log.Println(action)
				}
			}
		}
	}
}

func ResendRepeatable(fromPast bool) {
	var historyHours int64 = *HistoryHours
	if !fromPast {
		historyHours = 0
	}

	t := time.Now().Unix()
	// t300 := strconv.FormatInt(t-(t%300), 10) // modulo to nearest 5-minutes interval, like Mon, 02 Jul 2018 19:55:00 GMT

	end := t - (t % 300)
	start := end - (historyHours / 12)

	for i := start; start <= end; start += 300 {
		// log.Println(i, start, end)
		t300 := strconv.FormatInt(i, 10)
		tasks, _ := Client.SMembers("tasks-repeatable-" + t300).Result()

		if len(tasks) > 0 {
			log.Println("repeatable tasks", tasks)
			for _, task := range tasks {
				var action Action
				err := json.Unmarshal([]byte(task), &action)
				if err != nil {
					log.Println(err.Error())
				} else {
					if len(action.ParentUUID) == 0 {
						action.ParentUUID = action.UUID
					}
					u, _ := uuid.NewV4()
					var UUID = u.String()
					action.UUID = UUID
					action.Created = t - (t % 300)
					Client.SAdd("tasks-new", UUID)

					js, _ := json.Marshal(action)

					Client.Set("task/"+UUID, string(js), 0)
					Client.SAdd("user/tasks/"+action.Creator, UUID)

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

					Client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300new, 10), string(js))

					if action.Type != "task" {
						go Post("http://127.0.0.1:80/pub/mngrtasks", string(js))
					} else {
						go Post("http://127.0.0.1:80/pub/"+action.Target, string(js))
					}

					Client.SRem("tasks-repeatable-"+t300, task)
				}
			}
		}
	}
}

func CheckAlive() {
	zonds, _ := Client.SMembers("Zond-online").Result()
	if len(zonds) > 0 {
		// log.Println("active", zonds)
		for _, zond := range zonds {
			tp, _ := Client.Get(zond + "/alive").Result()
			// log.Println(zond, tp)
			if tp == "" {
				u, _ := uuid.NewV4()
				var UUID = u.String()
				var msec = time.Now().Unix()
				action := Action{Action: "alive", UUID: UUID, Created: msec}
				js, _ := json.Marshal(action)
				Client.Set(zond+"/alive", UUID, 90*time.Second)
				go Post("http://127.0.0.1:80/pub/zond:"+zond, string(js))
			} else {
				log.Println(zond, "— removed")
				Client.SRem("Zond-online", zond)

				Client.HDel("zond:city", zond)
				Client.HDel("zond:country", zond)
				Client.HDel("zond:asn", zond)
				go Delete("http://127.0.0.1:80/pub/zond:" + zond)
				GetActiveDestinations()
			}
		}
	}
}
