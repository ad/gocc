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
)

func StartSelfupdate(slug string, version string) {
	selfUpdateTicker := time.NewTicker(10 * time.Minute)
	go func(selfUpdateTicker *time.Ticker) {
		for {
			select {
			case <-selfUpdateTicker.C:
				if err := selfUpdate(slug, version); err != nil {
					fmt.Fprintln(os.Stderr, err)
					// os.Exit(1)
				}
			}
		}
	}(selfUpdateTicker)
}

func selfUpdate(slug string, version string) error {
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
