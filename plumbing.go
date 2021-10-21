package main

import (
	"fmt"
	"time"

	"github.com/blueforesticarus/goontunes/util"
)

func (self *Plumber) rescan() {
	self.j_spot_track.Pause(true)
	global.Spotify.ready.Wait()

	//who needs thread safety?
	//entries should only grow so we should be good taking a slice
	//for real though I don't know how to do the mutexs properly in this case
	self.pauseall(true)
	for _, e := range global.em.Entries[:] {
		//I mean plumb is designed so that race conditions dont matter
		PlumbEntry(e) // this will retry getting spotify data if it didn't before
	}
	self.pauseall(false) //unpause
	self.j_spot_track.Pause(false)
	self.j_playlist_task.Trigger()
}

type Plumber struct {
	//efficiently fetch data
	j_spot_track    util.Job
	j_spot_extra    util.Job
	j_spot_album    util.Job
	j_spot_playlist util.Job

	j_playlist_task util.Job

	save_em  util.CooldownJob
	save_lib util.CooldownJob
}

func new_Plumber() *Plumber {
	var p Plumber
	p.j_spot_album.Spinup(batchSpotAlbums, 20, 1, false)
	p.j_spot_track.Spinup(batchSpotTracks, 50, 1, false)
	p.j_spot_extra.Spinup(batchSpotExtra, 100, 1, false)

	p.j_spot_playlist.Spinup(func(s []string) {
		handleDiscoverWeekly(s[0])
	}, 1, 1, false)

	p.j_playlist_task.Spinup(func(_ []string) {
		//I want this to wait for other tasks to be empty
		//TODO okay so what I need is a entry task that waits on the other tasks
		util.WaitOnIdle(&p.j_spot_album, &p.j_spot_track)
		if p.j_playlist_task.None() { //short circuit
			DoPlaylistTask()
		}
	}, 1, 1, true)

	p.save_em.SpinupCooldown(global.em.save, time.Second*4)
	p.save_lib.SpinupCooldown(global.lib.save, time.Second*4)
	return &p
}

func (self *Plumber) pauseall(p bool) {
	self.j_spot_album.Pause(p)
	self.j_spot_extra.Pause(p)
	self.j_spot_track.Pause(p)
}

//called in discord.go when it processes an Entry, basically do everything.
func PlumbEntry(entry Entry) {
	if entry.ID == "" {
		return
	}
	if global.em.add_entry(entry) {
		fmt.Println("Plumb: ", entry.Url)
		global.plumber.save_em.Trigger()
	}

	if entry.IsTrack {
		PlumbTrack(entry.ID, entry.Service)
		return
	}

	if entry.Type == "playlist" {
		global.plumber.j_spot_playlist.Add(entry.ID)
		return
	}

	if entry.Service == "spotify" && entry.Type == "album" {
		c := global.lib.getCollection(entry.ID)

		if c == nil || len(global.lib.getTracks(c.ID)) == 0 {
			global.plumber.j_spot_album.Add(entry.ID)
		} else {
			PlumbCollection(c)
		}
	}
}

func PlumbCollection(c *Collection) {
	global.plumber.j_spot_extra.Pause(true)
	global.plumber.j_spot_track.Pause(true)
	for _, id := range c.TracksIDs {
		PlumbTrack(id, c.Service)
	}
	global.plumber.j_spot_extra.Pause(false)
	global.plumber.j_spot_track.Pause(false)
	global.plumber.save_lib.Trigger()
}

func batchSpotAlbums(ids []string) {
	global.Spotify.ready.Wait()
	cl := global.Spotify.fetch_album_tracks(ids)
OUTER:
	for i, c := range cl {
		if c.TracksIDs == nil || len(c.TracksIDs) == 0 /*0 len is more of a bug*/ {
			fmt.Printf("Plumb: missed tracks on album %s\n", ids[i])
		} else {
			for _, tid := range c.TracksIDs {
				if tid == "" {
					fmt.Printf("Plumb: missed a track on album %s\n", ids[i])
					continue OUTER
				}
			}
			global.lib.addCollection(c)
			PlumbCollection(&c)
		}
	}
	global.plumber.save_lib.Trigger()
}

func PlumbTrack(id string, service string) {
	if id == "" {
		return
	}

	track := global.lib.getTrack(id)
	if track == nil || len(track.IDs) == 0 {
		track = &Track{
			IDs:    []string{id},
			IDMaps: map[string]int{service: 0},
		}
		global.lib.addTrack(track)
	}

	if service == "spotify" {
		if track.SpotifyInfo == nil {
			global.plumber.j_spot_track.Add(id)
		}
		if track.SpotifyExtraInfo == nil {
			global.plumber.j_spot_extra.Add(id)
		}
	}
}

func batchSpotTracks(ids []string) {
	global.Spotify.ready.Wait()
	tl := global.Spotify.fetch_tracks_info(ids)
	for i, v := range tl {
		if v == nil {
			fmt.Printf("Plumb: missed info on track %s\n", ids[i])
		} else {
			track := global.lib.getTrack(v.ID.String())
			global.lib.Lock.Lock()
			track.SpotifyInfo = v
			global.lib.Lock.Unlock()
		}
	}
	global.plumber.save_lib.Trigger()
}

func batchSpotExtra(ids []string) {
	global.Spotify.ready.Wait()
	tl := global.Spotify.fetch_tracks_extrainfo(ids)
	for i, v := range tl {
		if v == nil {
			fmt.Printf("Plumb: missed extrainfo on track %s\n", ids[i])
		} else {
			track := global.lib.getTrack(v.ID.String())
			global.lib.Lock.Lock()
			track.SpotifyExtraInfo = v
			global.lib.Lock.Unlock()
		}
	}

	global.plumber.save_lib.Trigger()
}

func handleDiscoverWeekly(id string) {
	global.Spotify.ready.Wait()

	c := global.lib.getCollection(id)
	if c != nil && c.Service == "ignored" {
		c.TracksIDs = []string{}
		return
	}

	global.Spotify.ready.Wait()
	pl := global.Spotify.fetch_playlist(id)
	if c != nil && pl != nil && pl.SnapshotID == c.Rev {
		fmt.Printf("SPOTIFY: playlist %s has %d tracks\n", pl.ID, len(c.TracksIDs))
		PlumbCollection(c)
		return
	}

	if pl != nil {
		if pl.Owner.ID == "spotify" && pl.Name == "Discover Weekly" {
			c2 := global.Spotify.fetch_playlist_tracks(pl)
			if c2 != nil {
				global.lib.addCollection(*c2)
				PlumbCollection(c2)
			}
		} else {
			c2 := Collection{ID: id, TracksIDs: []string{}, Name: pl.Name, Service: "ignored"}
			global.lib.addCollection(c2)
			fmt.Printf("ignoring non discover weekly playlist %s\n", pl.Name)
		}
		global.plumber.save_lib.Trigger()
	} else {
		println("error")
	}
}

func DoPlaylistTask() {
	global.Spotify.ready.Wait()

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
