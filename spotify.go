package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"runtime"
	"sync"
	"unsafe"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

func fuckshit(Ids []string) []spotify.ID {
	return *(*[]spotify.ID)(unsafe.Pointer(&Ids))
}
func shitfuck(Ids []spotify.ID) []string {
	return *(*[]string)(unsafe.Pointer(&Ids))
}

func batched(foo func(int, []string), ls []string, n int, async bool) {
	var wg sync.WaitGroup
	bar := func(offset int, idss []string) {
		foo(offset, idss)
		wg.Done()
	}

	for i := 0; i < len(ls); i += n {
		if n > len(ls)-i {
			n = len(ls) - i
		}
		wg.Add(1)
		if async {
			go bar(i, ls[i:i+n])
		} else {
			foo(i, ls[i:i+n])
		}
	}
	if async {
		wg.Wait()
	}
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
	batched(foo, Ids, 50, false)
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
	batched(foo, Ids, 100, false)
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
	batched(foo, Ids, 20, false)
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

/*
func (self *SpotifyApp) reauthenticate(token *oauth2.Token) {
	ctx := context.Background()
	if token.Expiry.Before(time.Now()) { // expired so let's update it
		src := self.conf.TokenSource(ctx, token)
		newToken, err := src.Token() // this actually goes and renews the tokens
		if err != nil {
			panic(err)
		}
		if newToken.AccessToken != token.AccessToken {
			//saveToken(newToken) // back to the database with new access and refresh token
			token = newToken
		}
	}
	//client := config.Client(ctx, token)
}*/

func (self *SpotifyApp) authentication_flow() {
	ret := regexp.MustCompile(`localhost(:\d+)`).FindStringSubmatch(self.Redirect_Uri)
	if ret == nil {
		fmt.Printf("SPOTIFY: bad Redirect_Uri:%s must be localhost:port\n", self.Redirect_Uri)
		panic("")
	}
	port := ret[0]

	var auth = spotifyauth.New(
		spotifyauth.WithClientID(self.ClientID),
		spotifyauth.WithClientSecret(self.ClientSecret),
		spotifyauth.WithRedirectURL(self.Redirect_Uri),
		spotifyauth.WithScopes(spotifyauth.ScopeUserReadPrivate),
	)

	var ch = make(chan *spotify.Client)
	var state = "phadrus"

	server := &http.Server{Addr: port}
	callback := func(w http.ResponseWriter, r *http.Request) {
		//log.Println("Got request for:", r.URL.String())
		if r.URL.Path != "/" {
			return
		}

		tok, err := auth.Token(r.Context(), state, r)
		if err != nil {
			http.Error(w, "Couldn't get token", http.StatusForbidden)
			log.Fatal(err)
		}
		if st := r.FormValue("state"); st != state {
			http.NotFound(w, r)
			log.Fatalf("State mismatch: %s != %s\n", st, state)
		}

		// use the token to get an authenticated client
		client := spotify.New(
			auth.Client(r.Context(), tok),
			spotify.WithRetry(true),
		)
		fmt.Fprintf(w, "Login Completed!")
		ch <- client
	}

	// first start an HTTP server
	http.HandleFunc("/", callback)

	go func() {
		err := server.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	}()

	url := auth.AuthURL(state)

	{ // open in default browser
		var cmd string
		var args []string

		switch runtime.GOOS {
		case "windows":
			cmd = "cmd"
			args = []string{"/c", "start"}
		case "darwin":
			cmd = "open"
		default: // "linux", "freebsd", "openbsd", "netbsd"
			cmd = "xdg-open"
		}
		args = append(args, url)
		exec.Command(cmd, args...).Start()
	}

	// wait for auth to complete
	self.client = <-ch

	/*causes error
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel() //no clue what this does

	server.Shutdown(ctx)
	*/
}
