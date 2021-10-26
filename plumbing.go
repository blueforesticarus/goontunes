package main

import (
	"fmt"
	"time"

	"github.com/blueforesticarus/goontunes/util"
)

type Plumber struct {
	d_entry util.Delegate

	//efficiently fetch data
	d_collection util.Delegate
	j_spot_track util.WaitJob
	j_spot_extra util.WaitJob

	d_track         util.Delegate
	j_spot_album    util.WaitJob
	j_spot_playlist util.WaitJob

	j_playlist_task util.TriggerJob
	j_save_em       util.CooldownJob //implement
	j_save_lib      util.CooldownJob
}

func (self *Plumber) rescan() {
	self.d_entry.Set(1)
	defer self.d_entry.Set(-1)

	global.em.Lock.RLock()
	el := make([]Entry, len(global.em.Entries))
	for _, v := range global.em.Entries {
		el = append(el, v)
	}
	global.em.Lock.RUnlock()

	for _, v := range el {
		self.d_entry.Plumb(v)
	}
}

func new_Plumber() *Plumber {
	var p Plumber
	util.Pin.Add(1)

	//register plumb functions
	//plumb functions should be the only ones to edit lib
	p.d_collection.Foo = PlumbCollection
	p.d_track.Foo = PlumbTrack
	p.d_entry.Foo = PlumbEntry

	//create delegate deps
	p.d_collection.AddJob(&p.j_spot_album)
	p.d_collection.AddJob(&p.j_spot_playlist)

	p.d_track.AddJob(&p.j_spot_track)
	p.d_track.AddJob(&p.j_spot_extra)

	//generall deps
	util.Depends(&p.d_entry, &p.d_track)
	util.Depends(&p.d_entry, &p.d_collection)
	util.Depends(&p.d_collection, &p.d_track)

	/*
		still having deadlock issues
		they are easier to fix, but it means the model is still probably wrong

		ex) I was waiting on track_wait (which is tied to d_entry) in PlumbTrack
		but the PlumbEntry -> PlumbTrack is syncronous, so it deadlocks
		and it needs to be for there to not be deadtime in the wait for playlist task...
		at least under the current delegate model
	*/

	//spinup workers
	p.j_spot_track.FilterRepeats(IDString)
	p.j_spot_track.Spinup(batchSpotTracks, 50)

	p.j_spot_track.FilterRepeats(IDString)
	p.j_spot_extra.Spinup(batchSpotExtra, 100)

	p.j_spot_album.FilterRepeats(IDString)
	p.j_spot_album.Spinup(batchSpotAlbums, 20)

	p.j_spot_playlist.FilterRepeats(IDCollections)
	p.j_spot_playlist.Spinup(singleSpotPlaylist, 1)

	//playlist task
	util.Depends(&p.d_track, &p.j_playlist_task)
	p.j_playlist_task.Spinup(DoPlaylistTask)

	//save tasks
	p.j_save_em.Spinup(global.em.save, 10)
	p.j_save_lib.Spinup(global.lib.save, 10)

	util.Pin.Done()
	return &p
}

//called in discord.go when it processes an Entry, basically do everything.
func PlumbEntry(_entry interface{}) {
	var entry = _entry.(Entry)
	if !entry.CheckValid() {
		return
	}

	if global.em.add_entry(entry) {
		//fmt.Printf("Plumb URL: %s\n", entry.Url)
	}

	if entry.IsTrack {
		global.plumber.d_track.Plumb(make_track(entry.ID, entry.Service))
	} else {
		global.plumber.d_collection.Plumb(Collection{
			ID:      entry.ID,
			Service: entry.Service,
			Type:    entry.Type,
		})
	}

	global.plumber.j_playlist_task.Trigger()
	global.plumber.j_save_em.Trigger()
}

func PlumbCollection(_collection interface{}) {
	var collection = _collection.(Collection)
	collection.Valid(true)
	c, _ := global.lib.addCollection(collection)
	defer global.plumber.j_playlist_task.Trigger()
	defer global.plumber.j_save_lib.Trigger()

	if c.Ignored {
		return
	}

	if c.TracksIDs != nil {
		for _, t := range make_tracks(c.TracksIDs, c.Service) {
			global.plumber.d_track.Plumb(t)
		}
	}

	/*
		if global.lib._id_skip[c.ID] {
			return
		}
	*/

	if len(c.TracksIDs) != 0 {
		//reload playlist after 10 minutes, never reload album
		if c.Date.After(time.Now().Add(-time.Minute*10)) || c.Type == "Album" {
			return
		}
	}

	switch collection.Service {
	case "spotify":
		switch collection.Type {
		case "album":
			global.plumber.j_spot_album.Plumb(collection.ID)
		case "playlist":
			global.plumber.j_spot_playlist.Plumb(collection)
		}
	}
}

func batchSpotAlbums(_ids ...interface{}) {
	ids := ToStrings(_ids...)
	cl := global.Spotify.fetch_album_tracks(ids)

	for i, c := range cl {
		if len(c.TracksIDs) == 0 {
			fmt.Printf("Plumb: missed tracks on album %s\n", ids[i])

			//TODO: perhaps this belongs in the collection struct
			global.lib._id_skip[c.ID] = true
			continue
		}
		global.plumber.d_collection.Plumb(c)
	}
}

func singleSpotPlaylist(_collection ...interface{}) {
	c := ToCollections(_collection...)[0]

	pl := global.Spotify.fetch_playlist(c.ID)
	if pl != nil && pl.SnapshotID == c.Rev {
		fmt.Printf("SPOTIFY: playlist %s has %d tracks (unchanged)\n", pl.ID, len(c.TracksIDs))
		c.Date = time.Now()
		global.plumber.d_collection.Plumb(c)
		return
	}

	if pl == nil {
		fmt.Printf("SPOTIFY: error getting playlist")
		return
	}

	if pl.Owner.ID != "spotify" {
		c.Ignored = true
		c.Name = pl.Name
		c.Date = time.Now()
		fmt.Printf("ignoring non spotify playlist %s\n", pl.Name)
		global.plumber.d_collection.Plumb(c)
		return
	}

	c2 := global.Spotify.fetch_playlist_tracks(pl)

	if c2 != nil {
		c2.Date = time.Now()
		global.plumber.d_collection.Plumb(*c2)
	} else {
		fmt.Printf("SPOTIFY: error getting playlist tracks")
	}
}

//TODO all below
func PlumbTrack(_track interface{}) {
	var track = _track.(Track)
	track.Valid(true)

	t, _ := global.lib.addTrack(track)
	defer global.plumber.j_playlist_task.Trigger()
	defer global.plumber.j_save_lib.Trigger()

	spot_id, ok := t.GetId("spotify")
	if ok {
		if !t.SpotifyInfo.Initialized {
			global.plumber.j_spot_track.Plumb(spot_id)
		}
		if !t.SpotifyExtraInfo.Initialized {
			global.plumber.j_spot_extra.Plumb(spot_id)
		}
	}
}

func batchSpotTracks(_ids ...interface{}) {
	ids := ToStrings(_ids...)

	tl := global.Spotify.fetch_tracks_info(ids)
	if len(tl) == 0 {
		return
	}

	for _, v := range tl {
		track := make_track(v.ID.String(), "spotify")
		track.SpotifyInfo = Cached_SpotifyInfo{
			Date:        time.Now(),
			V:           v,
			Failed:      0,
			Initialized: true,
		}

		//Note: this reason this doesn't go round and round forever is because Plumb
		//on a delegate is called syncronously, PlumbTrack is therefore run before this
		//function exits, which means any loops will be stopped by the hot filter
		global.plumber.d_track.Plumb(track)
	}
}

func batchSpotExtra(_ids ...interface{}) {
	ids := ToStrings(_ids...)

	tl := global.Spotify.fetch_tracks_extrainfo(ids)
	if len(tl) == 0 {
		return
	}

	for _, v := range tl {
		track := make_track(v.ID.String(), "spotify")
		track.SpotifyExtraInfo = Cached_SpotifyExtraInfo{
			Date:        time.Now(),
			V:           v,
			Failed:      0,
			Initialized: true,
		}

		//See note in batchSpotTracks
		global.plumber.d_track.Plumb(track)
	}
}

func DoPlaylistTask() {
	for _, p := range global.Playlists {
		last := p.last_rebuild
		if !p.Rebuild() && p.last_rebuild.Sub(last) < time.Minute*30 {
			continue //TODO force rescan
		}

		for _, sp := range global.Spotify.Playlists {
			if sp.Sync == p.Name {
				sp.Update(p)
			}
		}
	}
}
