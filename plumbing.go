package main

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type Job struct {
	Lock  sync.Cond
	Queue []string

	hot sync.Map
	//die bool TODO killable
}

func (job *Job) spinup(worker func([]string), batchsize int, maxparallel uint32) {
	job.Queue = make([]string, 0, 50)
	job.Lock = *sync.NewCond(&sync.Mutex{})
	//job.Lock is signaled every time Ids is altered
	//we want to start the worker function any time
	//A: no workers are running and there is at least one thing in the queue
	//or B: the queue has batchsize items
	var active uint32

	foo := func(batch []string) {
		worker(batch)                         //must be blocking
		atomic.AddUint32(&active, ^uint32(0)) //decrement
		for _, s := range batch {
			job.hot.Delete(s)
		}
		job.Lock.Broadcast() //wakeup Wait in for loop
	}

	go func() {
		job.Lock.L.Lock() //must start locked

		for {
			if len(job.Queue) > 0 {
				if len(job.Queue) > batchsize {
					if active < maxparallel {
						atomic.AddUint32(&active, 1)
						go foo(job.Queue[:batchsize])
						job.Queue = job.Queue[batchsize:]
						continue
					}
				} else if active == 0 {
					atomic.AddUint32(&active, 1)
					go foo(job.Queue[:]) //pretty sure the next line WONT undo this
					job.Queue = make([]string, 0, 50)
				}
			}
			job.Lock.Wait()
		}
	}()

}

func (job *Job) add(id string) {
	_, hot := job.hot.LoadOrStore(id, true)
	if !hot {
		job.Lock.L.Lock()
		job.Queue = append(job.Queue, id)
		job.Lock.L.Unlock()

		job.Lock.Broadcast() //wakeup spinup
	}
}

type Plumber struct {
	//efficiently fetch data
	j_spot_track Job
	j_spot_extra Job
	j_spot_album Job
}

func new_Plumber() *Plumber {
	var p Plumber
	p.j_spot_album.spinup(batchSpotAlbums, 20, 1)
	p.j_spot_track.spinup(batchSpotTracks, 50, 1)
	p.j_spot_extra.spinup(batchSpotExtra, 100, 1)
	return &p
}

func batchSpotAlbums(ids []string) {
	global.Spotify.ready.Wait()
	cl := global.Spotify.fetch_album_tracks(ids)
	for i, c := range cl {
		if c.TracksIDs == nil {
			fmt.Printf("Plumb: missed tracks on album %s\n", ids[i])
		} else {

			global.lib.Lock.Lock()
			global.lib.Collections[c.ID] = c
			global.lib.Lock.Unlock()

			for _, t := range c.TracksIDs {
				PlumbTrack(t, "spotify")
			}
		}
	}
	global.lib.save() //TODO redo how this works
}

func batchSpotTracks(ids []string) {
	global.Spotify.ready.Wait()
	tl := global.Spotify.fetch_tracks_info(ids)
	for i, v := range tl {
		if v == nil {
			fmt.Printf("Plumb: missed info on track %s\n", ids[i])
		} else {
			track := global.lib.getTrack(v.ID.String())
			track.SpotifyInfo = v
		}
	}
	global.lib.save()
}

func batchSpotExtra(ids []string) {
	global.Spotify.ready.Wait()
	tl := global.Spotify.fetch_tracks_extrainfo(ids)
	for i, v := range tl {
		if v == nil {
			fmt.Printf("Plumb: missed extrainfo on track %s\n", ids[i])
		} else {
			track := global.lib.getTrack(v.ID.String())
			track.SpotifyExtraInfo = v
		}
	}

	global.lib.save()
}

//called in discord.go when it processes an Entry, basically do everything.
func PlumbEntry(entry Entry) {
	if global.em.add_entry(entry) {
		fmt.Println("Plumb: ", entry.Url)
	}

	if entry.IsTrack {
		PlumbTrack(entry.ID, entry.Service)

	} else {
		c := global.lib.Collections[entry.ID]
		if c.ID == "" && entry.Service == "spotify" && entry.Type == "album" {
			global.plumber.j_spot_album.add(entry.ID)
		}
	}
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
			global.plumber.j_spot_track.add(id)
		}
		if track.SpotifyExtraInfo == nil {
			global.plumber.j_spot_extra.add(id)
		}
	}
}

//Used by discord.go to know how far back to look for messages
func (em *EntryManager) Latest(platform string, channel string) Entry {
	em.Lock.Lock()
	latest := Entry{Valid: false, MessageId: ""}
	for _, entry := range em.Entries {
		if (platform == "" || platform == entry.Platform) &&
			(channel == "" || channel == entry.ChannelId) {
			if !latest.Valid || latest.Date.Before(entry.Date) {
				latest = entry
			}
		}
	}
	em.Lock.Unlock()
	return latest
}
