package main

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unsafe"

	"github.com/blueforesticarus/goontunes/util"
	"github.com/zmb3/spotify/v2"
)

func fuckshit(Ids []string) []spotify.ID {
	return *(*[]spotify.ID)(unsafe.Pointer(&Ids))
}
func shitfuck(Ids []spotify.ID) []string {
	return *(*[]string)(unsafe.Pointer(&Ids))
}

func SimplePl2Collection(pl spotify.SimplePlaylist) Collection {
	return Collection{
		Name:  pl.Name,
		ID:    pl.ID.String(),
		Owner: pl.Owner.ID,
		Rev:   pl.SnapshotID,
		Type:  "Playlist",
		Date:  time.Now(),
		Size:  int(pl.Tracks.Total),
	}
}

//used to find spotify links, see discord.py
func try_spotify(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}
	if strings.Contains(u.Host, "spotify") {
		entry.Service = "spotify"

		entry.Valid = true
		if strings.Contains(entry.Url, "album") {
			entry.Type = "album"
			entry.IsTrack = false
		} else if strings.Contains(entry.Url, "track") {
			entry.Type = "track"
			entry.IsTrack = true
		} else if strings.Contains(entry.Url, "user") {
			entry.Type = "user"
			entry.IsTrack = false
		} else if strings.Contains(entry.Url, "playlist") {
			entry.Type = "playlist"
			entry.IsTrack = false
		} else {
			entry.Valid = false
			return
		}

		re := regexp.MustCompile("/" + entry.Type + "/([0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz]+)")
		ms := re.FindStringSubmatch(u.Path)
		if len(ms) == 2 {
			entry.ID = ms[1]
		} else {
			entry.Valid = false
		}
	}

}

type SpotifyInfo = spotify.FullTrack
type SpotifyExtraInfo = spotify.AudioFeatures

type SpotifyApp struct {
	ClientID     string
	ClientSecret string
	Redirect_Uri string

	Playlists []*ServicePlaylist

	CacheToken bool

	client *spotify.Client
	userid string

	ready          util.OutputsNeedInit
	playlist_ready util.OutputsNeedInit
}

func (self *SpotifyApp) Name() string {
	return "spotify"
}

func (self *SpotifyApp) Connect() {
	if self.ClientID == "" || self.ClientID == "<yours>" {
		log.Fatalf("SPOTIFY: Generate a client id and secret following the instructions here: %s\n", helpurl)
	}

	var tokenfile string
	if self.CacheToken {
		//NOTE: this is the last remaining "global" in this file :(
		//... not actually a bad use for a global
		tokenfile = global.CachePath + "/spotify.token"
	}

	oauth_client := Authenticate(tokenfile, self.AuthConfig()) //see spotifyauth.go
	self.client = spotify.New(
		oauth_client,
		spotify.WithRetry(true),
	)

	// use the client, test if it is working
	user, err := self.client.CurrentUser(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("SPOTIFY: You are logged in as:", user.ID)
	self.userid = user.ID

	self.ready.Init(0)

	for _, p := range self.Playlists {
		p.Init(self)
	}

	self.playlist_ready.Init(0)
}

func (self *SpotifyApp) Get_Track_Id(track *Track) string {
	i, ok := track.IDMaps["spotify"]
	if ok {
		id := track.IDs[i]
		if id != "" && track.SpotifyInfo.V != nil {
			return id
		}
	}
	return ""
}

func (self *SpotifyApp) Playlist_InsertTracks(ID string, ins_list []Pl_Ins) int {
	//insert groups of tracks
	var position = 0
	var total = 0
	bar := func(o int, idss []string) {
		_, err := self.client.AddTracksToPlaylistOpt(
			context.Background(), spotify.ID(ID), position+o, fuckshit(idss)...,
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
	return total
}

func (self *SpotifyApp) Playlist_DeleteTracks(ID string, rm_list []Pl_Rm) int {
	//delete tracks
	total := 0
	foo := func(o int, n int) {

		spot_rml := make([]spotify.TrackToRemove, n)
		for i, v := range rm_list[o : o+n] {
			spot_rml[i] = spotify.NewTrackToRemove(v.id, []int{v.i})
		}

		_, err := self.client.RemoveTracksFromPlaylistOpt(
			context.Background(), spotify.ID(ID), spot_rml, "",
		)
		if err != nil {
			fmt.Printf("SPOTIFY: could not delete playlist tracks, %s\n", err)
		} else {
			fmt.Printf("SPOTIFY: deleted %d tracks, offset %d \n", n, o)
			total += n
		}
	}
	util.BatchedRange(foo, len(rm_list), 100)

	return total
}

func (self *SpotifyApp) List_Playlists() []Collection {
	return self.list_playlists(self.userid)
}

func (self *SpotifyApp) list_playlists(user string) []Collection {

	var ret = make([]Collection, 0, 50)

	pl, err := self.client.GetPlaylistsForUser(
		context.Background(),
		self.userid,
		spotify.Fields("href,limit,next,previous,total,offset,items(id,name,owner(id),description)"),
		spotify.Limit(50),
	)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get user playlists, %v\n", err)
		return ret
	}

	for {
		for _, p := range pl.Playlists {
			ret = append(ret, SimplePl2Collection(p))
		}

		if pl.Next == "" {
			break
		}

		err = self.client.NextPage(context.Background(), pl)
		if err != nil {
			fmt.Printf("SPOTIFY: err getting next page of user playlists, %v\n", err)
			break
		}
	}
	return ret
}

func (self *SpotifyApp) find_discover(name string) string {
	self.ready.Wait()

	for _, playlist := range self.list_playlists(name) {
		if playlist.Owner == "spotify" && playlist.Name == "Discover Weekly" {
			return string(playlist.ID) //probably breaks if users link have saved someone elses discover weekly
		}
	}
	return ""
}

func (self *SpotifyApp) Create_Playlist(name string) string {
	pl, err := self.client.CreatePlaylistForUser(context.Background(), self.userid, name, "GoonTunes", true, false)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get create playlist %s, %v\n", name, err)
		return ""
	}
	fmt.Printf("SPOTIFY: Created playlist %s %s\n", pl.ID.String(), pl.Name)
	return pl.ID.String()
}

func (self *SpotifyApp) Fetch_Playlist(ID string) *Collection {
	self.ready.Wait()
	if ID == "" {
		return nil
	}

	fields := spotify.Fields("name,snapshot_id,tracks(total),id,owner(id)")
	pl, err := self.client.GetPlaylist(context.Background(), spotify.ID(ID), fields)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get playlist %s metadata, %v\n", ID, err)
		return nil
	}
	//fmt.Printf("SPOTIFY: got playlist %s metadata\n", pl.ID) its too much
	var c = SimplePl2Collection(pl.SimplePlaylist)
	return &c
}

func (self *SpotifyApp) Fetch_Playlist_Tracks(c Collection) *Collection {
	self.ready.Wait()

	//NOTE: we are repeating work here done by the Fetch_Playlist calls
	fields := spotify.Fields("name,snapshot_id,tracks(href,limit,next,offset,previous,total,items(track(id))),id,owner(id)") //100 tracks per request, better than 50
	pl, err := self.client.GetPlaylist(context.Background(), spotify.ID(c.ID), fields)
	if err != nil {
		fmt.Printf("SPOTIFY: Failed to get playlist %s metadata, %v\n", c.ID, err)
		return nil
	}

	trackids := make([]string, 0, pl.Tracks.Total+2)

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

	fmt.Printf("SPOTIFY: got playlist %s with %d tracks\n", pl.ID, len(trackids))

	c = SimplePl2Collection(pl.SimplePlaylist)
	c.TracksIDs = trackids
	return &c
}

func (self *SpotifyApp) fetch_tracks_info(Ids []string) map[string]*SpotifyInfo {
	self.ready.Wait()

	ret := make(map[string]*SpotifyInfo, len(Ids))
	foo := func(offset int, idss []string) {
		tl, err := self.client.GetTracks(context.Background(), fuckshit(idss))
		if err != nil {
			fmt.Printf("SPOTIFY: Failed to get tracks, %v\n", err)
			return
		}
		for i, track := range tl {
			if track == nil {
				fmt.Printf("SPOTIFY: Missing track audio features %s\n", idss[i])
			} else {
				ret[string(track.ID)] = track
			}
		}
	}
	util.Batched(foo, Ids, 50, false)
	fmt.Printf("SPOTIFY: got info for %d tracks\n", len(Ids))
	return ret
}

func (self *SpotifyApp) fetch_tracks_extrainfo(Ids []string) map[string]*SpotifyExtraInfo {
	self.ready.Wait()

	ret := make(map[string]*SpotifyExtraInfo, len(Ids))
	foo := func(_ int, idss []string) {
		tl, err := self.client.GetAudioFeatures(context.Background(), fuckshit(idss)...)
		if err != nil {
			fmt.Printf("SPOTIFY: Failed to get track audio features, %v\n", err)
			return
		}
		for i, audiofeatures := range tl {
			if audiofeatures == nil {
				fmt.Printf("SPOTIFY: Missing track audio features %s\n", idss[i])
			} else {
				ret[string(audiofeatures.ID)] = audiofeatures
			}
		}
	}
	util.Batched(foo, Ids, 100, false) //redundant with plumber
	fmt.Printf("SPOTIFY: got extra info for %d tracks\n", len(Ids))
	return ret
}

func (self *SpotifyApp) fetch_album_tracks(Ids []string) []Collection {
	self.ready.Wait()

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

			ret[i+offset] = Collection{
				ID:        album.ID.String(),
				TracksIDs: trackids,
				Service:   "spotify",
				Date:      time.Now(),
				Type:      "album",
			}
		}

	}
	util.Batched(foo, Ids, 20, false)
	fmt.Printf("SPOTIFY: got tracks for %d albums\n", len(Ids))
	return ret
}
