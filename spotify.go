package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"unsafe"

	"github.com/blueforesticarus/goontunes/util"
	"github.com/lytics/base62"
	"github.com/pmezard/go-difflib/difflib"
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

	Playlists []*SpotifyPlaylist

	CacheToken bool

	client *spotify.Client
	ready  sync.WaitGroup
	userid string
}

type SpotifyPlaylist struct {
	ID   string
	Name string

	Sync string //the internal playlist to sync to
	//NoDelete bool TODO

	cache *Collection
}

func (self *SpotifyPlaylist) Scan() error {
	if self.ID == "" {
		return fmt.Errorf("Playlist %s has no known ID\n", self.Name)
	}

	//get metadata
	v := global.Spotify.fetch_playlist(self.ID)
	if v == nil {
		return fmt.Errorf("configured playlist %s unavailable\n", self.ID)
	}

	//update cache of playlist tracks
	if self.cache == nil || v.SnapshotID != self.cache.Rev {
		c := global.Spotify.fetch_playlist_tracks(v)
		if c == nil {
			return fmt.Errorf("could not update playlist cache")
		}
		self.cache = c
	}
	return nil
}

//higher level playlist update function
func (self *SpotifyPlaylist) Update(p *Playlist) {
	fmt.Printf("Begin sync of %s <-> %s\n", self.ID, p.Name)

	//assume p is correct playlist, and p.rebuild called
	err := self.Scan()
	if err != nil {
		fmt.Printf("Abort playlist update. %v \n", err)
		return
	}

	if len(p.tracks) == 0 {
		fmt.Printf("Abort playlist update. %v \n", "refusing to sync a empty playlist")
		return
	} else {
	}

	//get just the strings, filter things missing spot info
	target := make([]string, 0, len(p.tracks))
	for _, v := range p.tracks {
		if v == nil || v.IDMaps == nil {
			//bug
			println("Abort playlist sync error with track")
			return
		}
		i, ok := v.IDMaps["spotify"]

		id := v.IDs[i]
		if ok && id != "" {
			target = append(target, id)
		}
	}

	fmt.Printf("Attempting to sync %d/%d tracks\n", len(target), len(p.tracks))

	current := make([]string, len(self.cache.TracksIDs))
	for i, v := range self.cache.TracksIDs {
		current[i] = v
	}

	//compute delta
	rm_list := make([]spotify.TrackToRemove, 0, 100)

	type B struct {
		ids []string
		i   int
	}
	ins_list := make([]B, 0, 100)

	sm := difflib.NewMatcher(current, target)
	for _, v := range sm.GetOpCodes() {
		if v.Tag == 'd' || v.Tag == 'r' {
			for i := v.I1; i < v.I2; i++ {
				rm_list = append(rm_list, spotify.NewTrackToRemove(current[i], []int{i}))
			}
		}

		if v.Tag == 'i' || v.Tag == 'r' {
			asdf := B{target[v.J1:v.J2], v.J1}
			ins_list = append(ins_list, asdf)
		}
	}

	//sort strings so that we can not worry about how snapshot code works
	sort.Slice(ins_list, func(i, j int) bool {
		return ins_list[i].i < ins_list[j].i //insert last first
	})

	sort.Slice(rm_list, func(i, j int) bool {
		return rm_list[i].Positions[0] > rm_list[j].Positions[0] //sort reverse
	})

	//delete tracks
	foo := func(o int, n int) {
		_, err := global.Spotify.client.RemoveTracksFromPlaylistOpt(
			context.Background(), spotify.ID(self.ID), rm_list[o:o+n], "",
		)
		if err != nil {
			fmt.Printf("SPOTIFY: could not delete playlist tracks, %s\n", err)
		} else {
			fmt.Printf("SPOTIFY: deleted %d %d tracks\n", o, n)
		}
	}
	if len(rm_list) > 0 {
		util.BatchedRange(foo, len(rm_list), 100)
		fmt.Printf("SPOTIFY: updated playlist %s, %d deleted\n", self.ID, len(rm_list))
	}

	//insert groups of tracks
	var position = 0
	var total = 0
	bar := func(o int, idss []string) {
		_, err := global.Spotify.client.AddTracksToPlaylistOpt(
			context.Background(), spotify.ID(self.ID), position+o, fuckshit(idss)...,
		)
		if err != nil {
			fmt.Printf("SPOTIFY: could not insert playlist tracks, %s\n", err)
		} else {
			fmt.Printf("SPOTIFY: inserted %d tracks\n", len(idss))
			total += len(idss)
		}
	}

	for _, v := range ins_list {
		position = v.i
		util.Batched(bar, v.ids, 100, false)
	}
	if total > 0 {
		fmt.Printf("SPOTIFY: updated playlist %s, %d inserted\n", self.ID, total)
	}

	if total == 0 && len(rm_list) == 0 {
		fmt.Printf("playlist is already correct\n")
	}

	self.Scan()
	self.Check(target)
}

func (self *SpotifyPlaylist) Check(target []string) {
	if self.cache == nil || len(self.cache.TracksIDs) != len(target) {
		fmt.Printf("SPOTIFY: error detected in playlist update\n")
		return
	}
	for i, v := range self.cache.TracksIDs {
		if v != target[i] {
			fmt.Printf("SPOTIFY: error detected in playlist update\n")
		}
	}
}

func (self *SpotifyPlaylist) idcache() string {
	return global.CachePath + "/" + base62.StdEncoding.EncodeToString([]byte(self.Name))
}

func (self *SpotifyApp) connect() {
	self.ready.Add(1)
	if self.ClientID == "" || self.ClientID == "<yours>" {
		log.Fatalf("DISCORD: Generate a client id and secret following the instructions here: %s\n", helpurl)
	}

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
	self.userid = user.ID

	self.init_playlists()
	self.ready.Done()
}

func (self *SpotifyApp) init_playlists() {
	//TODO this is an example of a function where the prints would benefit from graph structure

	userplaylists := self.list_playlists(self.userid)

	for _, p := range self.Playlists {
		if p.ID == "" {
			if p.Name == "" {
				break //ignore
			}
			bytes, err := os.ReadFile(p.idcache())
			p.ID = string(bytes)

			if err != nil || self.fetch_playlist(p.ID) == nil {
				p.ID = string(userplaylists[p.Name].ID)
				if p.ID == "" {
					p.ID = self.create_playlist(p.Name)
				}
				os.WriteFile(p.idcache(), []byte(p.ID), 0644)
			}
		}

		v := self.fetch_playlist(p.ID)
		if v == nil {
			fmt.Printf("SPOTIFY: configured playlist %s unavailable\n", p.ID)
		} else {
			fmt.Printf("SPOTIFY: configured playlist %s found \"%s\"\n", p.ID, v.Name)
			c := self.fetch_playlist_tracks(v)
			if c != nil {
				p.cache = c
			}
		}
	}
}

func (self *SpotifyApp) list_playlists(user string) map[string]spotify.SimplePlaylist {
	var ret = make(map[string]spotify.SimplePlaylist)

	pl, err := self.client.GetPlaylistsForUser(
		context.Background(),
		user,
		spotify.Fields("href,limit,next,previous,total,offset,items(id,name,owner(id),description)"),
		spotify.Limit(50),
	)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get user playlists, %v\n", err)
		return ret
	}

	for {
		for _, p := range pl.Playlists {
			ret[p.Name] = p
			//println(p.Name, p.Owner.ID)
		}

		if pl.Next == "" {
			break
		}

		err = self.client.NextPage(context.Background(), pl)
		if err != nil {
			fmt.Printf("SPOTIFY: err getting next page of album tracks, %v\n", err)
			break
		}
	}
	return ret
}

func (self *SpotifyApp) create_playlist(name string) string {
	pl, err := self.client.CreatePlaylistForUser(context.Background(), self.userid, name, "GoonTunes", true, false)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get create playlist %s, %v\n", name, err)
		return ""
	}
	fmt.Printf("SPOTIFY: Created playlist %s %s\n", pl.ID.String(), pl.Name)
	return pl.ID.String()
}

func (self *SpotifyApp) find_discover(name string) string {
	for _, playlist := range self.list_playlists(name) {
		if playlist.Owner.ID == "spotify" && playlist.Name == "Discover Weekly" {
			return string(playlist.ID) //probably breaks if users link have saved someone elses discover weekly
		}
	}
	return ""
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
			size := album.Tracks.Total
			trackids := make([]string, 0, size)
		OUTER:
			for {
				for _, track := range album.Tracks.Tracks {
					if track.ID.String() == "" {
						fmt.Printf("SPOTIFY: bad album tracklist\n")
						break OUTER
					}
					trackids = append(trackids, track.ID.String())
				}

				if album.Tracks.Next == "" {
					break
				}

				err := self.client.NextPage(context.Background(), &album.Tracks)
				if err != nil {
					fmt.Printf("SPOTIFY: err getting next page of playlist tracks, %v\n", err)
					break
				}
			}

			ret[i+offset] = Collection{ID: album.ID.String(), TracksIDs: trackids, Service: "spotify"}
		}

	}
	util.Batched(foo, Ids, 20, false)
	fmt.Printf("SPOTIFY: got tracks for %d albums\n", len(Ids))
	return ret
}

func (self *SpotifyApp) fetch_playlist(ID string) *spotify.FullPlaylist {
	//fields := spotify.Fields("name,snapshot_id,tracks(href,limit,next,offset,previous,total,items(track(id))),id,owner(id)") //100 tracks per request, better than 50
	pl, err := self.client.GetPlaylist(context.Background(), spotify.ID(ID)) //fields)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get playlist %s metadata, %v\n", ID, err)
		return nil
	}
	//fmt.Printf("SPOTIFY: got playlist %s metadata\n", pl.ID) its too much
	return pl
}

func (self *SpotifyApp) fetch_playlist_tracks(pl *spotify.FullPlaylist) *Collection {
	if pl == nil {
		return nil
	}

	size := pl.Tracks.Total
	trackids := make([]string, 0, size)

	for {
		for _, track := range pl.Tracks.Tracks {
			//index := i + pl.Tracks.Offset
			if track.Track.ID.String() == "" {
				fmt.Printf("SPOTIFY: bad playlist tracklist\n")
				return nil
			}
			trackids = append(trackids, track.Track.ID.String())
		}

		if pl.Tracks.Next == "" {
			break
		}

		err := self.client.NextPage(context.Background(), &pl.Tracks)
		if err != nil {
			fmt.Printf("SPOTIFY: err getting next page of playlist tracks, %v\n", err)
			return nil
		}
	}

	ret := Collection{
		ID:        pl.ID.String(),
		Rev:       pl.SnapshotID,
		Service:   "spotify",
		TracksIDs: trackids,
		Name:      pl.Name,
	}
	fmt.Printf("SPOTIFY: got playlist %s with %d tracks\n", pl.ID, len(trackids))
	return &ret
}
