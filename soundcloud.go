package main

import (
	"github.com/yanatan16/golang-soundcloud/soundcloud"
)

// there is no lib :|
type SoundcloudApp struct {
	ClientID     string
	ClientSecret string
	Redirect_Uri string

	Playlists []*ServicePlaylist

	client soundcloud.Api
}
