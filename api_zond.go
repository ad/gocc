package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gorilla/csrf"
	uuid "github.com/nu7hatch/gouuid"
)

func ApiZondCreateHandler(w http.ResponseWriter, r *http.Request) {
	// if r.Method == "POST" {
	name := r.FormValue("name")

	u, _ := uuid.NewV4()
	var UUID = u.String()
	var msec = time.Now().Unix()

	if len(name) == 0 {
		name = UUID
	}

	Client.SAdd("zonds", UUID)

	userUUID, _ := Client.Get("user/UUID/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUUID == "" {
		u, _ := uuid.NewV4()
		userUUID = u.String()
		Client.Set(fmt.Sprintf("user/UUID/%s", r.Header.Get("X-Forwarded-User")), userUUID, 0)
	}

	zond := Zond{UUID: UUID, Name: name, Created: msec, Creator: userUUID}
	js, _ := json.Marshal(zond)

	Client.Set("zonds/"+UUID, string(js), 0)
	Client.SAdd("user/zonds/"+userUUID, UUID)

	log.Println("Zond created", UUID)

	// if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
	// w.Header().Set("X-CSRF-Token", csrf.Token(r))
	fmt.Fprintf(w, `{"status": "ok", "UUID": "%s"}`, UUID)
	// } else {
	// 	ShowCreateForm(w, r, UUID)
	// }
	// } else {
	// 	ShowCreateForm(w, r, "")
	// }
}

func ApiShowMyZonds(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	if page <= 0 {
		page = 1
	}
	userUuid, _ := Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := Client.SCard("user/zonds/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := GetPaginator(int(page), int(count), perPage)

	var results []Zond
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = Client.SScan("user/zonds/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			// log.Println(keys)
			for i, val := range keys {
				keys[i] = "zonds/" + val
			}

			items, _ := Client.MGet(keys...).Result()
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
			// log.Println(len(results), count, results)
		}
		// log.Println(len(results), count, currentPage, cursor, cursorNew, perPage)
	}

	varmap := map[string]interface{}{
		"results":  results,
		"count":    count,
		"pages":    pages,
		"page":     page,
		"has_prev": hasPrev,
		"has_next": hasNext,
	}

	js, _ := json.Marshal(varmap)

	w.Header().Set("X-CSRF-Token", csrf.Token(r))
	fmt.Fprintf(w, `%s`, js)
}
