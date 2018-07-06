package utils

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ad/gocc/ccredis"
	"github.com/ad/gocc/structs"

	"golang.org/x/crypto/bcrypt"
)

func IPToWSChannels(ip string) string {
	var result []string
	// result = append(result, "IP:"+ip)

	// TODO: receive addr from start args
	url := "http://127.0.0.1:9001/?ip=" + ip

	spaceClient := http.Client{
		Timeout: time.Second * 5, // Maximum of 2 secs
	}

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Println(err)
	} else {
		res, getErr := spaceClient.Do(req)
		if getErr != nil {
			log.Println(getErr)
		} else {

			body, readErr := ioutil.ReadAll(res.Body)
			if readErr != nil {
				log.Println(readErr)
			} else {

				geodata := structs.Geodata{}
				// log.Println(geodata)
				jsonErr := json.Unmarshal(body, &geodata)
				if jsonErr != nil {
					log.Println(jsonErr)
				} else {

					if geodata.City != "" {
						result = append(result, "City:"+geodata.City)
					}
					if geodata.Country != "" {
						result = append(result, "Country:"+geodata.Country)
					}
					if geodata.AutonomousSystemNumber != 0 {
						result = append(result, "ASN:"+fmt.Sprint(geodata.AutonomousSystemNumber))
					}
				}
			}
		}
	}
	result = append(result, "tasks")
	return strings.Join(result[:], ",")
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), 14)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func RandStr(len int) string {
	buff := make([]byte, len)
	rand.Read(buff)
	str := base64.StdEncoding.EncodeToString(buff)
	// Base 64 can be longer than len
	return str[:len]
}

// Get Fully Qualified Domain Name
func FQDN() string {
	hostname, err := os.Hostname()
	if err != nil {
		return "unknown"
	}

	addrs, err := net.LookupIP(hostname)
	if err == nil {
		for _, addr := range addrs {
			if ipv4 := addr.To4(); ipv4 != nil {
				ip, err := ipv4.MarshalText()
				if err != nil {
					return hostname
				}
				hosts, err := net.LookupAddr(string(ip))
				if err != nil || len(hosts) == 0 {
					return hostname
				}
				fqdn := hosts[0]
				return strings.TrimSuffix(fqdn, ".")
			}
		}
	}
	return hostname
}

func GetActiveDestinations() {
	zonds, _ := ccredis.Client.SMembers("Zond-online").Result()
	// if len(zonds) > 0 {
	// 	// for _, zond := range zonds {
	// }

	cities, _ := ccredis.Client.HVals("zond:city").Result()
	if len(cities) > 0 {
		cities = SliceUniqMap(cities)
	}

	countries, _ := ccredis.Client.HVals("zond:country").Result()
	if len(countries) > 0 {
		countries = SliceUniqMap(countries)
	}

	asns, _ := ccredis.Client.HVals("zond:asn").Result()
	if len(asns) > 0 {
		asns = SliceUniqMap(asns)
	}

	// log.Println(zonds, cities, countries, asns)

	channels := structs.Channels{Action: "destinations", Zonds: zonds, Countries: countries, Cities: cities, ASNs: asns}
	js, _ := json.Marshal(channels)
	// log.Println(string(js))

	go Post("http://127.0.0.1:80/pub/destinations", string(js))
}

func SliceUniqMap(s []string) []string {
	seen := make(map[string]struct{}, len(s))
	j := 0
	for _, v := range s {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		s[j] = v
		j++
	}
	return s[:j]
}

func GetPaginator(page int, total_count int, per_page int) (currentPage int, pages int, hasPrev bool, hasNext bool) {
	pages = int(math.Ceil(float64(total_count) / float64(per_page)))
	if page > pages {
		page = pages
	} else if page < 1 {
		page = 1
	}

	hasPrev = page > 1
	hasNext = page < pages

	currentPage = page

	return
}

func Post(url string, jsonData string) string {
	var jsonStr = []byte(jsonData)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Println(err)
		return "err"
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return string(body)
}

func Delete(url string) string {
	req, err := http.NewRequest("DELETE", url, nil)

	client := &http.Client{}
	resp, err := client.Do(req)
	if resp != nil {
		defer resp.Body.Close()
	}
	if err != nil {
		log.Println(err)
		return "err"
	}
	return "ok"
}
