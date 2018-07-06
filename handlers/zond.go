package handlers

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
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

func ZondCreateHandler(w http.ResponseWriter, r *http.Request) {
	// if r.Method == "POST" {
	name := r.FormValue("name")

	u, _ := uuid.NewV4()
	var Uuid = u.String()
	var msec = time.Now().Unix()

	if len(name) == 0 {
		name = Uuid
	}

	ccredis.Client.SAdd("zonds", Uuid)

	userUuid, _ := ccredis.Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		ccredis.Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	zond := structs.Zond{Uuid: Uuid, Name: name, Created: msec, Creator: userUuid}
	js, _ := json.Marshal(zond)

	ccredis.Client.Set("zonds/"+Uuid, string(js), 0)
	ccredis.Client.SAdd("user/zonds/"+userUuid, Uuid)

	log.Println("Zond created", Uuid)

	// if r.Header.Get("X-Requested-With") == "xmlhttprequest" {
	// w.Header().Set("X-CSRF-Token", csrf.Token(r))
	fmt.Fprintf(w, `{"status": "ok", "uuid": "%s"}`, Uuid)
	// } else {
	// 	ShowCreateForm(w, r, Uuid)
	// }
	// } else {
	// 	ShowCreateForm(w, r, "")
	// }
}

func ZondPong(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		body, err := ioutil.ReadAll(r.Body)

		if err != nil {
			http.Error(w, "Error reading request body", http.StatusInternalServerError)
		} else {
			var t structs.Action
			err := json.Unmarshal(body, &t)
			if err != nil {
				log.Println(err.Error())
			}
			// log.Println("pong from", t.ZondUuid, r.Header.Get("X-Forwarded-For"))
			tp, _ := ccredis.Client.Get(t.ZondUuid + "/alive").Result()
			if t.Uuid == tp {
				ccredis.Client.Del(t.ZondUuid + "/alive")
				// log.Print(t.ZondUuid, "Zond pong")
				// w.Header().Set("X-CSRF-Token", csrf.Token(r))
				fmt.Fprintf(w, `{"status": "ok"}`)
			}
		}
	}
}

func ZondSub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-ZondUuid")
	if len(uuid) > 0 {
		log.Println(uuid, "— connected")
		ccredis.Client.SAdd("Zond-online", uuid)
		usersCount, _ := ccredis.Client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)

		for i := 0; i < 5; i++ {
			var data = r.Header.Get("X-Channel-Id" + fmt.Sprint(i))
			if strings.HasPrefix(data, "City") {
				var city = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HSet("zond:city", uuid, city)
			} else if strings.HasPrefix(data, "Country") {
				var country = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HSet("zond:country", uuid, country)
			} else if strings.HasPrefix(data, "ASN") {
				var asn = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HSet("zond:asn", uuid, asn)
			}
		}
		utils.GetActiveDestinations()
	}
}

func ZondUnsub(w http.ResponseWriter, r *http.Request) {
	var uuid = r.Header.Get("X-ZondUuid")
	if len(uuid) > 0 {
		log.Println(r.Header.Get("X-ZondUuid"), "— disconnected")
		ccredis.Client.SRem("Zond-online", r.Header.Get("X-ZondUuid"))
		usersCount, _ := ccredis.Client.SCard("Zond-online").Result()
		fmt.Printf("Active zonds: %d\n", usersCount)

		for i := 0; i < 5; i++ {
			var data = r.Header.Get("X-Channel-Id" + fmt.Sprint(i))
			if strings.HasPrefix(data, "City") {
				// var city = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HDel("zond:city", uuid)
			} else if strings.HasPrefix(data, "Country") {
				// var country = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HDel("zond:country", uuid)
			} else if strings.HasPrefix(data, "ASN") {
				// var asn = strings.Join(strings.Split(r.Header.Get("X-Channel-Id"+fmt.Sprint(i)), ":")[1:], ":")
				ccredis.Client.HDel("zond:asn", uuid)
			}
		}
		utils.GetActiveDestinations()
	}
}

func ShowMyZonds(w http.ResponseWriter, r *http.Request) {
	var perPage int = 20
	page, _ := strconv.ParseInt(r.FormValue("page"), 10, 0)
	userUuid, _ := ccredis.Client.Get("user/uuid/" + r.Header.Get("X-Forwarded-User")).Result()
	if userUuid == "" {
		u, _ := uuid.NewV4()
		userUuid = u.String()
		ccredis.Client.Set(fmt.Sprintf("user/uuid/%s", r.Header.Get("X-Forwarded-User")), userUuid, 0)
	}

	count, _ := ccredis.Client.SCard("user/zonds/" + userUuid).Result()
	currentPage, pages, hasPrev, hasNext := utils.GetPaginator(int(page), int(count), perPage)

	var results []structs.Zond
	if count > 0 {
		// log.Println(count)
		var cursor = uint64(int64(perPage) * int64(currentPage-1))
		// var cursorNew uint64
		var keys []string
		var err error
		keys, _, err = ccredis.Client.SScan("user/zonds/"+userUuid, cursor, "", int64(perPage)).Result()

		if err != nil {
			log.Println(err)
		} else {
			// log.Println(keys)
			for i, val := range keys {
				keys[i] = "zonds/" + val
			}

			items, _ := ccredis.Client.MGet(keys...).Result()
			for _, val := range items {
				if val != nil {
					var t structs.Zond
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

	pager := pagination.New(int(count), perPage, currentPage, "/my/zonds")

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

	tmpl, _ := templ.New("zonds", bindata.Asset).Parse("zonds.html")
	tmpl.Execute(w, varmap)
}
