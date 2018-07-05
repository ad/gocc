package selfupdate

import (
	"fmt"
	"log"
	"os"
	"syscall"
	"time"

	"github.com/blang/semver"
	"github.com/kardianos/osext"
	"github.com/rhysd/go-github-selfupdate/selfupdate"

	"github.com/ad/gocc/utils"
)

func StartSelfupdate(slug string, version string, fqdn string) {
	selfUpdateTicker := time.NewTicker(5 * time.Minute)
	go func(selfUpdateTicker *time.Ticker) {
		for {
			select {
			case <-selfUpdateTicker.C:
				if err := selfUpdate(slug, version, fqdn); err != nil {
					fmt.Fprintln(os.Stderr, err)
					// os.Exit(1)
				}
			}
		}
	}(selfUpdateTicker)
}

func selfUpdate(slug string, version string, fqdn string) error {
	previous := semver.MustParse(version)
	latest, err := selfupdate.UpdateSelf(previous, slug)
	if err != nil {
		return err
	}

	if previous.Equals(latest.Version) {
		// fmt.Println("Current binary is the latest version", version)
	} else {
		fmt.Println("Update successfully done to version", latest.Version)
		fmt.Println("Release note:\n", latest.ReleaseNotes)

		go utils.Post("http://127.0.0.1:80/pub/"+fqdn, `{"action": "updated", "version": "`+fmt.Sprint(latest.Version)+`"}`)

		file, err := osext.Executable()
		if err != nil {
			return err
		}
		err = syscall.Exec(file, os.Args, os.Environ())
		if err != nil {
			log.Fatal(err)
		}
	}

	return nil
}
