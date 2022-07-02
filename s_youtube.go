package main

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"

	"github.com/blueforesticarus/goontunes/util"
	"google.golang.org/api/youtube/v3"
)

/*
TODO unit test
https://www.youtube.com/watch?v=0zM3nApSvMg&feature=feedrec_grec_index
https://www.youtube.com/user/IngridMichaelsonVEVO#p/a/u/1/QdK8U-VIH_o
https://www.youtube.com/v/0zM3nApSvMg?fs=1&amp;hl=en_US&amp;rel=0
https://www.youtube.com/watch?v=0zM3nApSvMg#t=0m10s
https://www.youtube.com/embed/0zM3nApSvMg?rel=0
https://www.youtube.com/watch?v=0zM3nApSvMg
https://youtu.be/0zM3nApSvMg
*/

func try_youtube(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}
	if strings.Contains(u.Host, "youtube") || strings.Contains(u.Host, "youtu.be") {
		entry.Service = "youtube"
		re := regexp.MustCompile(`(?:[?&]v=|\/embed\/|\/1\/|\/v\/|https:\/\/(?:www\.)?youtu\.be\/)([^&\n?#]+)`)
		ms := re.FindStringSubmatch(entry.Url)
		if len(ms) == 2 {
			entry.ID = ms[1]
			entry.Valid = true
			entry.IsTrack = true
		} else {
			entry.Valid = false
		}
	}
}

type YoutubeInfo struct {
}

type YoutubeApp struct {
	Auth       string
	Playlists  []*ServicePlaylist
	CacheToken bool

	client *youtube.Service
	userid string

	ready util.OutputsNeedInit
}

func (self *YoutubeApp) Connect() {
	if self.Auth == "" || self.Auth == "<yours>" {
		log.Fatalf("YOUTUBE: Generate a client id and secret following the instructions here: %s\n", helpurl)
	}

	var tokenfile string
	if self.CacheToken {
		//NOTE: this is the last remaining "global" in this file :(
		//... not actually a bad use for a global
		tokenfile = global.CachePath + "/youtube.token"
	}

	oauth_client := Authenticate(tokenfile, self.AuthConfig()) //see spotifyauth.go
	service, err := youtube.New(oauth_client)
	if err != nil {
		//TODO
		return
	}

	self.client = service
	self.ready.Init(0)
	fmt.Println("Youtube Ready")
}
