package main

import (
	"fmt"
	"log"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"regexp"
	"strings"
)

var smtphost = "127.0.0.1"
var smtpport = "25"
var smtpusername = ""
var smtppassword = ""
var smtpauth = smtp.PlainAuth("", smtpusername, smtppassword, smtphost)
var smtpaddr = smtphost + ":" + smtpport

func SendMail(to string, subject string, body string, hostname string) {
	if hostname == "" {
		hostname, _ = os.Hostname()
	}

	fromName := "no-reply@cc"
	fromEmail := "no-reply@" + hostname
	toNames := []string{to}
	toEmails := []string{to}
	// Build RFC-2822 email
	toAddresses := []string{}
	for i, _ := range toEmails {
		to := mail.Address{toNames[i], toEmails[i]}
		toAddresses = append(toAddresses, to.String())
	}
	toHeader := strings.Join(toAddresses, ", ")
	from := mail.Address{fromName, fromEmail}
	fromHeader := from.String()
	subjectHeader := subject
	header := make(map[string]string)
	header["To"] = toHeader
	header["From"] = fromHeader
	header["Subject"] = subjectHeader
	header["Content-Type"] = `text/html; charset="UTF-8"`
	msg := ""
	for k, v := range header {
		msg += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	msg += "\r\n" + body
	bMsg := []byte(msg)
	// Send using local postfix service
	c, err := smtp.Dial(smtpaddr)
	if err != nil {
		log.Println(err)
		return
	}
	defer c.Close()
	if err = c.Mail(fromHeader); err != nil {
		log.Println(err)
		return
	}
	for _, addr := range toEmails {
		if err = c.Rcpt(addr); err != nil {
			log.Println(err)
			return
		}
	}
	w, err := c.Data()
	if err != nil {
		log.Println(err)
		return
	}
	_, err = w.Write(bMsg)
	if err != nil {
		log.Println(err)
		return
	}
	err = w.Close()
	if err != nil {
		log.Println(err)
		return
	}
	err = c.Quit()
	// Or alternatively, send with remote service like Amazon SES
	// err = smtp.SendMail(addr, auth, fromEmail, toEmails, bMsg)
	// Handle response from local postfix or remote service
	if err != nil {
		log.Println(err)
		return
	}
	log.Println("mail sent")
}

var (
	userRegexp    = regexp.MustCompile("^[a-zA-Z0-9!#$%&'*+/=?^_`{|}~.-]+$")
	hostRegexp    = regexp.MustCompile("^[^\\s]+\\.[^\\s]+$")
	userDotRegexp = regexp.MustCompile("(^[.]{1})|([.]{1}$)|([.]{2,})")
	emailRegexp   = regexp.MustCompile(`^[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,4}$`)
)

func ValidateEmail(email string) bool {
	return emailRegexp.MatchString(email)
}

// Validate checks format of a given email and resolves its host name.
func Validate(email string) bool {
	if len(email) < 6 || len(email) > 254 {
		return false
	}

	at := strings.LastIndex(email, "@")
	if at <= 0 || at > len(email)-3 {
		return false
	}

	user := email[:at]
	host := email[at+1:]

	if len(user) > 64 {
		return false
	}
	if userDotRegexp.MatchString(user) || !userRegexp.MatchString(user) || !hostRegexp.MatchString(host) {
		return false
	}

	switch host {
	case "localhost", "example.com":
		return true
	}

	if _, err := net.LookupMX(host); err != nil {
		if _, err := net.LookupIP(host); err != nil {
			return false
		}
	}

	return true
}

// Normalize normalizes email address.
func Normalize(email string) string {
	email = strings.TrimSpace(email)
	email = strings.TrimRight(email, ".")
	email = strings.ToLower(email)

	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		if parts[1] == "gmail.com" || parts[1] == "googlemail.com" {
			parts[1] = "gmail.com"
			parts[0] = strings.Split(replacePattern(parts[0], `\.`, ""), "+")[0]
		}
	}
	return strings.Join(parts, "@")
}

func replacePattern(str, pattern, replace string) string {
	r, _ := regexp.Compile(pattern)
	return r.ReplaceAllString(str, replace)
}
