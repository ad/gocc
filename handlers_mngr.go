package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"

	pagination "github.com/AndyEverLie/go-pagination-bootstrap"
	templ "github.com/arschles/go-bindata-html-template"
	"github.com/gorilla/csrf"
	uuid "github.com/nu7hatch/gouuid"
)

func MngrPong(w http.ResponseWriter, r *http.Request) {
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
			tp, _ := Client.Get(t.MngrUUID + "/alive").Result()
			if t.UUID == tp {
				Client.Del(t.MngrUUID + "/alive")
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "ok"}`)
			}
		}
	}
}

func MngrSub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-MngrUuid")
	if len(uuid) == 36 {
		isMember, _ := Client.SIsMember("mngrs", uuid).Result()
		if isMember {
			log.Println(uuid, "— connected")
			Client.SAdd("mngr-online", uuid)
			usersCount, _ := Client.SCard("mngr-online").Result()
			fmt.Printf("Active Mngrs: %d\n", usersCount)
		}
	}
}

func MngrUnsub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-MngrUuid")
	if len(uuid) > 0 {
		log.Println(r.Header.Get("X-MngrUuid"), "— disconnected")
		Client.SRem("Mngr-online", r.Header.Get("X-MngrUuid"))
		usersCount, _ := Client.SCard("mngr-online").Result()
		fmt.Printf("Active Mngrs: %d\n", usersCount)
	}
}

func ShowMyMngrs(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	userUuid, _ := Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := Client.SCard("user/mngrs/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := GetPaginator(int(page), int(count), perPage)

	var results []Mngr
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = Client.SScan("user/mngrs/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			// log.Println(keys)
			for i, val := range keys {
				keys[i] = "mngrs/" + val
			}

			items, _ := Client.MGet(keys...).Result()
			for _, val := range items {
				if val != nil {
					var t Mngr
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

	pager := pagination.New(int(count), perPage, currentPage, "/my/mngrs")

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

	tmpl, _ := templ.New("mngrs", Asset).Parse("mngrs.html")
	tmpl.Execute(w, varmap)
}
