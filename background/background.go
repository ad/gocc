package background

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ad/gocc/ccredis"
	"github.com/ad/gocc/structs"
	"github.com/ad/gocc/utils"

	uuid "github.com/nu7hatch/gouuid"
)

func ResetProcessing() {
	tasks, _ := ccredis.Client.SMembers("tasks-process").Result()
	if len(tasks) > 0 {
		for _, task := range tasks {
			s := strings.Split(task, "/")
			ZondUuid, taskUuid := s[0], s[1]

			tp, _ := ccredis.Client.Get(task + "/processing").Result()
			if tp != "1" {
				log.Println("Removed outdated task", tp, ZondUuid, taskUuid)
				ccredis.Client.SRem("zond-busy", ZondUuid)
				ccredis.Client.SRem("tasks-process", task)

				ccredis.Client.SAdd("tasks-new", taskUuid)

				js, _ := ccredis.Client.Get("task/" + taskUuid).Result()
				var action structs.Action
				err := json.Unmarshal([]byte(js), &action)
				if err != nil {
					log.Println(err.Error())
				} else {
					go utils.Post("http://127.0.0.1:80/pub/"+action.Target, string(js))
					log.Println("Task resend to queue", action)
				}
			}
		}
	}
}

func ResendOffline() {
	tasks, _ := ccredis.Client.SMembers("tasks-new").Result()

	if len(tasks) > 0 {
		log.Println("active tasks", tasks)
		for _, task := range tasks {
			js, _ := ccredis.Client.Get("task/" + task).Result()

			var action structs.Action
			err := json.Unmarshal([]byte(js), &action)
			if err != nil {
				log.Println(err.Error())
			} else {
				go utils.Post("http://127.0.0.1:80/pub/"+action.Target, string(js))
				log.Println(action)
			}
		}
	}
}

func ResendRepeatable() {
	t := time.Now().Unix()
	t300 := strconv.FormatInt(t-(t%300), 10) // modulo to nearest 5-minutes interval, like Mon, 02 Jul 2018 19:55:00 GMT

	tasks, _ := ccredis.Client.SMembers("tasks-repeatable-" + t300).Result()

	if len(tasks) > 0 {
		log.Println("repeatable tasks", tasks)
		for _, task := range tasks {
			var action structs.Action
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
				ccredis.Client.SAdd("tasks-new", Uuid)

				js, _ := json.Marshal(action)

				ccredis.Client.Set("task/"+Uuid, string(js), 0)
				ccredis.Client.SAdd("user/tasks/"+action.Creator, Uuid)

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

				ccredis.Client.SAdd("tasks-repeatable-"+strconv.FormatInt(t300new, 10), string(js))

				go utils.Post("http://127.0.0.1:80/pub/"+action.Target, string(js))

				ccredis.Client.SRem("tasks-repeatable-"+t300, task)
			}
		}
	}
}

func CheckAlive() {
	zonds, _ := ccredis.Client.SMembers("Zond-online").Result()
	if len(zonds) > 0 {
		// log.Println("active", zonds)
		for _, zond := range zonds {
			tp, _ := ccredis.Client.Get(zond + "/alive").Result()
			// log.Println(zond, tp)
			if tp == "" {
				u, _ := uuid.NewV4()
				var Uuid = u.String()
				var msec = time.Now().Unix()
				action := structs.Action{Action: "alive", Uuid: Uuid, Created: msec}
				js, _ := json.Marshal(action)
				ccredis.Client.Set(zond+"/alive", Uuid, 90*time.Second)
				go utils.Post("http://127.0.0.1:80/pub/zond:"+zond, string(js))
			} else {
				log.Println(zond, "— removed")
				ccredis.Client.SRem("Zond-online", zond)

				ccredis.Client.HDel("zond:city", zond)
				ccredis.Client.HDel("zond:country", zond)
				ccredis.Client.HDel("zond:asn", zond)
				go utils.Delete("http://127.0.0.1:80/pub/zond:" + zond)
				utils.GetActiveDestinations()
			}
		}
	}
}
