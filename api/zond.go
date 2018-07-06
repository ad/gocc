package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ad/gocc/ccredis"
	"github.com/ad/gocc/structs"
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
