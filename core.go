package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"
	"time"
)

type Entry struct {
	//message info
	Platform   string
	ChannelId  string
	PosterId   string
	PosterName string
	MessageId  string
	Date       time.Time
	Url        string

	//link info
	Service string
	Type    string
	IsTrack bool
	ID      string

	Valid bool
}

type EntryManager struct {
	Entries []Entry
	Lock    sync.RWMutex
	Sorted  bool

	//_entry_map map[string]*Entry
	cachepath string
}

func new_EntryManager() *EntryManager {
	var em EntryManager
	em.Entries = make([]Entry, 0)
	return &em
}

func (self *EntryManager) add_entry(entry Entry) bool {
	self.Lock.RLock()
	defer self.Lock.RUnlock()

	for i, e := range self.Entries {
		if e.MessageId == entry.MessageId {
			self.Entries[i] = entry
			return false
		}
	}
	self.Entries = append(self.Entries, entry)
	return true
}

func (self *EntryManager) save() {
	self.Lock.RLock()
	defer self.Lock.RUnlock() //supposedly defer has overhead

	bytes, _ := json.MarshalIndent(self.Entries, "", "")
	_ = ioutil.WriteFile(self.cachepath, bytes, 0644)
	fmt.Printf("saved entries\n")
}

func (self *EntryManager) load() {
	self.Lock.Lock()
	defer self.Lock.Unlock()

	bytes, err := ioutil.ReadFile(self.cachepath)
	if err != nil {
		fmt.Printf("failed to open %s\n", self.cachepath)
		return
	}

	err = json.Unmarshal([]byte(bytes), &self.Entries)
	if err != nil {
		fmt.Printf("failed to parse %s\n", self.cachepath)
	} else {
		fmt.Printf("loaded %s\n", self.cachepath)
	}
}

//Used by discord.go to know how far back to look for messages
func (em *EntryManager) Latest(platform string, channel string) Entry {
	em.Lock.RLock()
	defer em.Lock.RUnlock()

	latest := Entry{Valid: false, MessageId: ""}
	for _, entry := range em.Entries {
		if (platform == "" || platform == entry.Platform) &&
			(channel == "" || channel == entry.ChannelId) {
			if !latest.Valid || latest.Date.Before(entry.Date) {
				latest = entry
			}
		}
	}
	return latest
}

type Library struct {
	Tracks      []*Track //can contain nill if track removed
	Collections map[string]Collection
	Lock        sync.RWMutex

	_track_map map[string]*Track
	cachepath  string
}

func new_Library() *Library {
	var lib Library
	lib.Tracks = make([]*Track, 0)
	lib.Collections = make(map[string]Collection, 0)
	lib._track_map = make(map[string]*Track, 0)
	return &lib
}

func (self *Library) save() {
	self.Lock.RLock()
	defer self.Lock.RUnlock()

	bytes, _ := json.MarshalIndent(self, "", "")
	_ = ioutil.WriteFile(self.cachepath, bytes, 0644)
	fmt.Printf("saved library\n")
}

func (self *Library) load() {
	self.Lock.Lock()
	defer self.Lock.Unlock()

	bytes, err := ioutil.ReadFile(self.cachepath)
	if err != nil {
		fmt.Printf("failed to open %s\n", self.cachepath)
		return
	}

	err = json.Unmarshal([]byte(bytes), &self)
	if err != nil {
		fmt.Printf("failed to parse %s\n", self.cachepath)
	} else {
		fmt.Printf("loaded %s\n", self.cachepath)
	}
}

func (self *Library) getTrack(id string) *Track {
	self.Lock.RLock()
	track := self._track_map[id]
	if track != nil {
		if !contains_a_fucking_string(track.IDs, id) {
			panic("uh oh")
		}
		self.Lock.RUnlock()
		return track
	}

	for _, track := range self.Tracks {
		if contains_a_fucking_string(track.IDs, id) {
			self.Lock.RUnlock()
			self.Lock.Lock()
			self._track_map[id] = track
			self.Lock.Unlock()
			return track
		}
	}
	self.Lock.RUnlock()
	return nil
}

func (self *Library) getTracks(id string) []*Track {
	//no lock because it would deadlock when getTrack locks
	self.Lock.RLock()
	c := self.Collections[id]
	self.Lock.RUnlock()

	if c.ID != "" {
		tl := make([]*Track, len(c.TracksIDs))
		for i, t := range c.TracksIDs {
			tl[i] = self.getTrack(t)
		}
		return tl
	} else {
		return []*Track{self.getTrack(id)}
	}
}

func (self *Library) addTrack(track *Track) {
	self.Lock.Lock()
	defer self.Lock.Unlock()

	track.Index = len(self.Tracks)
	self.Tracks = append(self.Tracks, track)
}

func (self *Library) mergeTrack(t1 *Track, t2 *Track) {
	self.Lock.Lock()
	defer self.Lock.Unlock()

	if self.Tracks[t2.Index] == t2 {
		self.Tracks[t2.Index] = nil
	}

	if t1.SpotifyInfo == nil {
		t1.SpotifyInfo = t2.SpotifyInfo
		t1.SpotifyExtraInfo = t2.SpotifyExtraInfo
	}
	if t1.YoutubeInfo == nil {
		t1.YoutubeInfo = t2.YoutubeInfo
	}

	for _, t2_id := range t2.IDs {
		if !contains_a_fucking_string(t1.IDs, t2_id) {
			t1.IDs = append(t1.IDs, t2_id)
		}
	}
	for t2_service, t2_ii := range t2.IDMaps {
		ii := fucking_index(t1.IDs, t2.IDs[t2_ii])
		if ii == -1 {
			panic("barg")
		}
		t1.IDMaps[t2_service] = ii
	}
}

//Eventually this should be replaced with universe.
type Track struct {
	SpotifyInfo      *SpotifyInfo
	SpotifyExtraInfo *SpotifyExtraInfo

	YoutubeInfo *YoutubeInfo

	Index  int            //internal index
	IDs    []string       //all IDs (maybe multible youtube videos)
	IDMaps map[string]int //Service -> which ID in IDs
	/*
		isn't this be in __Info structs, yes! but this list contains the info
		(and only the info) needed to map Entry to Track. Note. that we fully
		rely on ids from platforms never having conflicts,
		I just kinda assume they are differnt sizes, sue me

		Use this ONLY for mapping, this is NOT for finding data later.
	*/
}

type Collection struct {
	//This exists to map the IDs of album posts to the tracks they contain
	//Unlike Track, these are not unified across services.
	//Once again, we just hope there are no cross service ID conflicts
	ID        string
	TracksIDs []string
}

type Playlist struct {
	Name     string
	Channels []string //nil for all channels
	NoRepeat bool

	Targets []string //which services
	tracks  []*Track

	SpotifyId string
	YoutubeId string
}
