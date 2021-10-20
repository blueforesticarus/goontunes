package main

import (
	"fmt"
	"time"

	"github.com/blueforesticarus/radio/util"
)

func (self *Plumber) rescan() {
	//who needs thread safety?
	//entries should only grow so we should be good taking a slice
	//for real though I don't know how to do the mutexs properly in this case
	self.pauseall(true)
	for _, e := range global.em.Entries[:] {
		//I mean plumb is designed so that race conditions dont matter
		PlumbEntry(e) // this will retry getting spotify data if it didn't before
	}
	self.pauseall(false) //unpause
}

type Plumber struct {
	//efficiently fetch data
	j_spot_track util.Job
	j_spot_extra util.Job
	j_spot_album util.Job

	save_em  util.CooldownJob
	save_lib util.CooldownJob
}

func new_Plumber() *Plumber {
	var p Plumber
	p.j_spot_album.Spinup(batchSpotAlbums, 20, 1, false)
	p.j_spot_track.Spinup(batchSpotTracks, 50, 1, false)
	p.j_spot_extra.Spinup(batchSpotExtra, 100, 1, false)

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
	if global.em.add_entry(entry) {
		fmt.Println("Plumb: ", entry.Url)
	}

	if entry.IsTrack {
		PlumbTrack(entry.ID, entry.Service)

	} else {
		global.lib.Lock.RLock()
		c := global.lib.Collections[entry.ID]
		global.lib.Lock.RUnlock()

		if (c.ID == "" || len(c.TracksIDs) == 0) && entry.Service == "spotify" && entry.Type == "album" {
			global.plumber.j_spot_album.Add(entry.ID)
		} else {
			global.plumber.j_spot_extra.Pause(true)
			global.plumber.j_spot_track.Pause(true)
			for _, id := range c.TracksIDs {
				PlumbTrack(id, "spotify")
			}
			global.plumber.j_spot_extra.Pause(false)
			global.plumber.j_spot_track.Pause(false)
		}
	}

	global.plumber.save_em.Trigger()
}

func batchSpotAlbums(ids []string) {
	global.Spotify.ready.Wait()
	cl := global.Spotify.fetch_album_tracks(ids)
	for i, c := range cl {
		if c.TracksIDs == nil || len(c.TracksIDs) == 0 /*0 len is more of a bug*/ {
			fmt.Printf("Plumb: missed tracks on album %s\n", ids[i])
		} else {
			global.lib.Lock.Lock()
			global.lib.Collections[c.ID] = c
			global.lib.Lock.Unlock()

			global.plumber.j_spot_extra.Pause(true)
			global.plumber.j_spot_track.Pause(true)
			for _, t := range c.TracksIDs {
				PlumbTrack(t, "spotify")
			}
			global.plumber.j_spot_extra.Pause(false)
			global.plumber.j_spot_track.Pause(false)
		}
	}
	global.plumber.save_lib.Trigger()
}

func PlumbTrack(id string, service string) {
	track := global.lib.getTrack(id)
	if track == nil {
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
			global.lib.Lock.RLock()
			track.SpotifyInfo = v
			global.lib.Lock.RUnlock()
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
			global.lib.Lock.RLock()
			track.SpotifyExtraInfo = v
			global.lib.Lock.RUnlock()
		}
	}

	global.plumber.save_lib.Trigger()
}
