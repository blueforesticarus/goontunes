package main

import (
	"net/url"
	"strings"

	"github.com/yanatan16/golang-soundcloud/soundcloud"
)

func try_soundcloud(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}

	if strings.Contains(u.Host, "soundcloud") {
		entry.Service = "soundcloud"
		entry.IsTrack = true
		entry.ID = u.Path //XXX RawPath?
		entry.Valid = true
	}
}

// there is no lib :|
type SoundcloudApp struct {
	ClientID     string
	ClientSecret string
	Redirect_Uri string

	Playlists []*ServicePlaylist

	client soundcloud.Api
}
