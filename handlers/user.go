package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"../bindata"
	"../ccredis"

	"github.com/ad/gocc/mail"
	"github.com/ad/gocc/utils"
	templ "github.com/arschles/go-bindata-html-template"
	"github.com/gorilla/csrf"
	"github.com/gorilla/securecookie"
	uuid "github.com/nu7hatch/gouuid"
)

var (
	nsCookieName         = "NSLOGIN"
	nsCookieHashKey      = []byte("SECURE_COOKIE_HASH_KEY")
	nsRedirectCookieName = "NSREDIRECT"
)

func AuthHandler(w http.ResponseWriter, r *http.Request) {
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

func UserInfoHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, `%[1]s at %[2]s (%[3]s)`, r.Header.Get("X-Forwarded-User"), Fqdn, Version)
}

func UserAuthHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, Version)
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
			hash, _ := ccredis.Client.Get("user/pass/" + login).Result()

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

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("login", bindata.Asset).Parse("login.html")
	tmpl.Execute(w, varmap)
}

func UserRegisterHandler(w http.ResponseWriter, r *http.Request) {
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
			hash, _ := ccredis.Client.Get("user/pass/" + login).Result()
			if hash != "" {
				errorMessage = "already registered"
				// var redirectURL = r.URL.Host + "/login"
				// http.Redirect(w, r, redirectURL, http.StatusFound)
			} else {
				password := utils.RandStr(12)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				ccredis.Client.Set("user/pass/"+login, hash, 0)

				u, _ := uuid.NewV4()
				var Uuid = u.String()
				ccredis.Client.Set("user/uuid/"+login, Uuid, 0)

				go mail.SendMail(login, "Your password", "password: "+password, Fqdn)

				var s = securecookie.New(nsCookieHashKey, nil)
				value := map[string]string{
					"user": login,
				}

				// encode username to secure cookie
				if encoded, err := s.Encode(nsCookieName, value); err == nil {
					ccredis.Client.Set("user/session/"+encoded, login, 0)
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

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("register", bindata.Asset).Parse("register.html")
	tmpl.Execute(w, varmap)
}

func UserRecoverHandler(w http.ResponseWriter, r *http.Request) {
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
			hash, _ := ccredis.Client.Get("user/pass/" + login).Result()
			if hash == "" {
				errorMessage = "user not found"
			} else {
				password := utils.RandStr(32)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				ccredis.Client.Set("user/recover/"+login, hash, 0)

				go mail.SendMail(login, "Reset password", `<a href="http://`+Fqdn+`/reset?hash=`+password+`&email=`+login+`">Click to reset</a>`, Fqdn)

				errorMessage = "Email with recovery link sent"
			}
		}
	}

	varmap := map[string]interface{}{
		"ErrorMessage":   errorMessage,
		csrf.TemplateTag: csrf.TemplateField(r),
	}
	tmpl, _ := templ.New("password_recovery", bindata.Asset).Parse("password_recovery.html")
	tmpl.Execute(w, varmap)
}

func UserResetHandler(w http.ResponseWriter, r *http.Request) {
	var redirectURL = r.URL.Host + "/user"

	password := r.FormValue("hash")
	password = strings.Replace(password, " ", "+", -1)
	login := r.FormValue("email")
	login = mail.Normalize(login)
	if !mail.Validate(login) {
		login = ""
	}

	if login != "" {
		hash, _ := ccredis.Client.Get("user/recover/" + login).Result()
		if hash != "" {
			res := utils.CheckPasswordHash(password, hash)
			if !res {
				log.Println("hash mistmatched", password, login)
			}
			if res {
				password = utils.RandStr(12)
				hash, _ = utils.HashPassword(password)
				// log.Println(login, password, hash)

				ccredis.Client.Set("user/pass/"+login, hash, 0)

				go mail.SendMail(login, "Your new password", "password: "+password, Fqdn)

				var s = securecookie.New(nsCookieHashKey, nil)
				value := map[string]string{
					"user": login,
				}

				// encode username to secure cookie
				if encoded, err := s.Encode(nsCookieName, value); err == nil {
					ccredis.Client.Set("user/session/"+encoded, login, 0)
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
