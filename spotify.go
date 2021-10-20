package main

import (
	"context"
	"fmt"
	"log"
	"sync"
	"unsafe"

	"github.com/blueforesticarus/radio/util"
	"github.com/zmb3/spotify/v2"
)

func fuckshit(Ids []string) []spotify.ID {
	return *(*[]spotify.ID)(unsafe.Pointer(&Ids))
}
func shitfuck(Ids []spotify.ID) []string {
	return *(*[]string)(unsafe.Pointer(&Ids))
}

type SpotifyInfo = spotify.FullTrack
type SpotifyExtraInfo = spotify.AudioFeatures

type SpotifyApp struct {
	ClientID     string
	ClientSecret string
	Redirect_Uri string

	Playlists map[string]string //name -> id

	CacheToken bool

	client *spotify.Client
	ready  sync.WaitGroup
}

func (self *SpotifyApp) fetch_tracks_info(Ids []string) []*SpotifyInfo {
	ret := make([]*SpotifyInfo, len(Ids))
	foo := func(offset int, idss []string) {
		a := fuckshit(idss)
		tl, err := self.client.GetTracks(context.Background(), a)
		if err != nil {
			fmt.Printf("SPOTIFY: Failed to get tracks, %v\n", err)
			return
		}
		for i, track := range tl {
			ret[offset+i] = track
		}
	}
	util.Batched(foo, Ids, 50, false)
	fmt.Printf("SPOTIFY: got info for %d tracks\n", len(Ids))
	return ret
}

func (self *SpotifyApp) fetch_tracks_extrainfo(Ids []string) []*SpotifyExtraInfo {
	ret := make([]*SpotifyExtraInfo, len(Ids))
	foo := func(offset int, idss []string) {
		tl, err := self.client.GetAudioFeatures(context.Background(), fuckshit(idss)...)
		if err != nil {
			fmt.Printf("SPOTIFY: Failed to get track audio features, %v\n", err)
			return
		}
		for i, audiofeatures := range tl {
			ret[offset+i] = audiofeatures
		}
	}
	util.Batched(foo, Ids, 100, false)
	fmt.Printf("SPOTIFY: got extra info for %d tracks\n", len(Ids))
	return ret
}

func (self *SpotifyApp) fetch_album_tracks(Ids []string) []Collection {
	// This will return an empty list for any album which has more than 50 tracks
	// this is due to track limits in spotifys api, and because at that point its more like a playlist, which is ignored.
	ret := make([]Collection, len(Ids))

	foo := func(offset int, idss []string) {
		al, err := self.client.GetAlbums(context.Background(), fuckshit(idss))
		if err != nil {
			fmt.Printf("SPOTIFY: Failed to get album tracks, %v\n", err)
			return
		}
		for i, album := range al {
			if album.Tracks.Total > 50 {
				fmt.Printf("SPOTIFY: Album %s has too many tracks, ignoring\n", album.Name)
			}
			for album.Tracks.Total != len(album.Tracks.Tracks) {
				err = self.client.NextPage(context.Background(), &album.Tracks)
				if err != nil {
					fmt.Printf("SPOTIFY: err getting next page of album tracks, %v\n", err)
					break
				}
			}
			trackids := make([]string, len(album.Tracks.Tracks))
			for i, track := range album.Tracks.Tracks {
				trackids[i] = track.ID.String()
			}

			ret[i+offset] = Collection{ID: album.ID.String(), TracksIDs: trackids}
		}
	}
	util.Batched(foo, Ids, 20, false)
	fmt.Printf("SPOTIFY: got tracks for %d albums\n", len(Ids))
	return ret
}

func (self *SpotifyApp) connect() {
	self.ready.Add(1)

	var tokenfile string
	if self.CacheToken {
		tokenfile = global.CachePath + "/spotify.token"
	}
	self.client = self.Authenticate(tokenfile) //see spotifyauth.go

	// use the client, test if it is working
	user, err := self.client.CurrentUser(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("SPOTIFY: You are logged in as:", user.ID)
	self.ready.Done()
}
