package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"
	"gopkg.in/yaml.v2"
)

var configfile = ".config"
var helpurl = "https://github.com/blueforesticarus/goontunes"

type State struct {
	Discord   *DiscordApp
	Spotify   *SpotifyApp
	Youtube   *YoutubeApp
	Matrix    *MatrixApp
	Playlists []Playlist

	CachePath string
	em        *EntryManager
	lib       *Library

	plumber *Plumber

	Manual map[string][]string
}

//Used by discord.go to know how far back to look for messages
func (global *State) Latest(platform string, channel string) string {
	global.em.Lock.RLock()
	defer global.em.Lock.RUnlock()

	latest := Entry{Valid: false, MessageId: ""}
	for _, entry := range global.em.Entries {
		if (platform == "" || platform == entry.Platform) &&
			(channel == "" || channel == entry.ChannelId) {
			if !latest.Valid || latest.Date.Before(entry.Date) {
				latest = entry
			}
		}
	}
	return latest.MessageId
}

func process_manual() {
	for k, v := range global.Manual {
		for i, s := range v {
			var entry = Entry{
				MessageId: fmt.Sprintf("manual+%s+%d", k, i),
				Platform:  "manual",
				ChannelId: k,
				Url:       s,
			}
			process_entry(&entry)
			if entry.Valid {
				global.plumber.d_entry.Plumb(entry)
			}
		}
	}
}

var global State

func main() {
	bytes, err := ioutil.ReadFile(configfile)
	if err != nil {
		fmt.Printf("Error, cannot open config file %v\n", configfile)
		return
	}

	err = yaml.UnmarshalStrict(bytes, &global)
	if err != nil {
		fmt.Printf("Could not parse config %v\n", err)
		fmt.Printf("Follow instructions at %s\n", helpurl)
		return
	}

	err = os.MkdirAll(global.CachePath, 0755)
	if err != nil {
		fmt.Printf("Could not create cachepath %s, %v\n", global.CachePath, err)
		return
	}

	/* Doesnt work
	runlock := global.CachePath + "/runlock"
	_, err = net.Listen("unix", runlock)
	if err != nil {
		log.Fatalf("Program already running. (If it isn't, delete %s)\n", runlock)
	}
	*/

	global.em = new_EntryManager()
	global.em.cachepath = global.CachePath + "/entries"
	global.em.load()

	global.lib = new_Library()
	global.lib.cachepath = global.CachePath + "/library"
	global.lib.load()

	global.plumber = new_Plumber()

	for _, app := range []App{
		global.Spotify,
		global.Youtube,
		global.Discord,
		global.Matrix,
	} {
		//XXX is this allowed? how do interfaces work?
		if ! app.IsNil() {
			app.Connect()
		}
	}

	go process_manual()

	global.plumber.rescan() // no  reason not to do this concurrent with Discord init, I think

	c := cron.New()
	c.AddFunc("@every 1h", func() {
		fmt.Printf("1hr timer\n")
		global.plumber.rescan()
	})
	c.Start()

	exitSignal := make(chan os.Signal)
	signal.Notify(exitSignal, syscall.SIGINT, syscall.SIGTERM)
	<-exitSignal
}