package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"

	"github.com/zmb3/spotify/v2"
	"golang.org/x/oauth2"
)

/*
based on the following:
https://github.com/zmb3/spotify/blob/v2.0.0/auth/auth.go#L194
https://github.com/golang/oauth2/issues/84
*/

const (
	AuthURL  = "https://accounts.spotify.com/authorize"
	TokenURL = "https://accounts.spotify.com/api/token"
)

func (app *SpotifyApp) AuthConfig() *oauth2.Config {
	cfg := &oauth2.Config{
		ClientID:     app.ClientID,
		ClientSecret: app.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  AuthURL,
			TokenURL: TokenURL,
		},
		RedirectURL: app.Redirect_Uri,
	}

	cfg.Scopes = append(cfg.Scopes,
		"playlist-read-private",
		"playlist-read-collaborative",
		"playlist-modify-public",
		"playlist-modify-private",
		"user-library-modify",
		"user-library-read",
	)
	return cfg
}

func WriteToken(cachefile string, token *oauth2.Token) {
	if cachefile != "" {
		bytes, err := json.Marshal(token)
		err = os.WriteFile(cachefile, bytes, 0644)
		if err != nil {
			fmt.Printf("could not save spotify token, %v", err)
		}
	}
}

func ReadToken(cachefile string) (*oauth2.Token, error) {
	// will return err for cachefile = ""
	data, err := os.ReadFile(cachefile)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	err = json.Unmarshal(data, &token)
	if err != nil {
		fmt.Printf("could not load spotify token, %v\n", err)
		return nil, err
	} else {
		return &token, nil
	}
}

type CachedTokenSource struct {
	src       oauth2.TokenSource
	CacheFile string
}

func (self CachedTokenSource) Token() (*oauth2.Token, error) {
	tok, err := self.src.Token()
	if err != nil {
		return nil, err
	}

	WriteToken(self.CacheFile, tok)
	return tok, nil
}

func (app *SpotifyApp) Authenticate(cachefile string) *spotify.Client {
	var ctx = context.Background() //no clue about this one
	var config = app.AuthConfig()

	tokensource := func(token *oauth2.Token) oauth2.TokenSource {
		var src CachedTokenSource

		src.src = config.TokenSource(ctx, token)
		src.CacheFile = cachefile

		return oauth2.ReuseTokenSource(token, src)
	}

	var src oauth2.TokenSource
	tok, err := ReadToken(cachefile)
	if err == nil {
		src = tokensource(tok)
		tok, err = src.Token()
		if err != nil {
			fmt.Printf("saved spotify token is invalid")
		}
	}

	if err != nil {
		tok = authentication_flow(config) //prompt user, assume success
		WriteToken(cachefile, tok)

		src = tokensource(tok)
	} else {
		fmt.Printf("spotify using cached token\n")
	}

	// use the token to get an authenticated client
	client := spotify.New(
		oauth2.NewClient(ctx, src),
		spotify.WithRetry(true),
	)

	return client
}

// EAS: no idea what this does
// see: https://github.com/zmb3/spotify/issues/20
func contextWithHTTPClient(ctx context.Context) context.Context {
	tr := &http.Transport{
		TLSNextProto: map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
	}
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Transport: tr})
}

func open_url_in_browser(url string) {
	fmt.Printf("\tYou should be redirected here: %s\n\tIf not open the link manually in your browser.\n", url)

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

func authentication_flow(config *oauth2.Config) *oauth2.Token {
	ret := regexp.MustCompile(`localhost(:\d+)`).FindStringSubmatch(config.RedirectURL)
	if ret == nil {
		fmt.Printf("SPOTIFY: bad Redirect_Uri:%s must be localhost:port\n", config.RedirectURL)
		panic("")
	}
	port := ret[0]
	state := "phadrus"

	ch := make(chan *oauth2.Token)

	callback := func(w http.ResponseWriter, r *http.Request) {
		//log.Println("Got request for:", r.URL.String())
		if r.URL.Path != "/" {
			return
		}

		values := r.URL.Query()
		if e := values.Get("error"); e != "" {
			log.Fatal(errors.New("spotify: auth failed - " + e))
		}

		code := values.Get("code")
		if code == "" {
			http.Error(w, "Couldn't get token", http.StatusForbidden)
			log.Fatal(errors.New("spotify: didn't get access code"))
		}

		if rstate := values.Get("state"); rstate != state {
			http.NotFound(w, r)
			log.Fatalf("State mismatch: %s != %s\n", rstate, state)
		}

		ctx := contextWithHTTPClient(r.Context())
		token, err := config.Exchange(ctx, code)

		if err != nil {
			log.Fatal(err)
		}
		ch <- token
		fmt.Fprintf(w, "Login Completed!")
	}

	http.HandleFunc("/", callback)
	server := &http.Server{Addr: port}
	go func() {
		//start an HTTP server
		err := server.ListenAndServe()
		if err != nil {
			log.Fatal(err)
		}
	}()

	url := config.AuthCodeURL(state)
	open_url_in_browser(url) //user grants permissions

	return <-ch //wait and return the token
}
