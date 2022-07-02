package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/blueforesticarus/goontunes/util"
	"github.com/google/go-cmp/cmp"
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

func (self *Entry) CheckValid() bool {
	if !self.Valid {
		return false
	}
	if self.Service == "" || self.ID == "" || self.MessageId == "" || self.Platform == "" {
		b, _ := json.MarshalIndent(self, "", "\t")
		fmt.Printf("Bad Track : %s\n", string(b))
		self.Valid = false
	}
	return self.Valid
}

type EntryManager struct {
	Entries map[string]Entry
	Lock    sync.RWMutex

	cachepath string
}

func new_EntryManager() *EntryManager {
	var em EntryManager
	em.Entries = make(map[string]Entry, 0)
	return &em
}

func (self *EntryManager) add_entry(entry Entry) bool {
	self.Lock.Lock()
	defer self.Lock.Unlock()

	id := entry.Platform + entry.MessageId

	e, ok := self.Entries[id]
	if ok && e.CheckValid() {
		self.Entries[id] = entry
		return false
	} else {
		self.Entries[id] = entry
		return true
	}
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

	for k, v := range self.Entries {
		v.CheckValid()
		self.Entries[k] = v
	}
}

type Library struct {
	Tracks      []*Track //can contain nill if track removed
	Collections map[string]*Collection
	Lock        sync.RWMutex

	_track_map map[string]int
	_id_skip   map[string]bool
	cachepath  string
}

func new_Library() *Library {
	var lib Library
	lib.Tracks = make([]*Track, 0)
	lib.Collections = make(map[string]*Collection)
	lib._track_map = make(map[string]int)
	lib._id_skip = make(map[string]bool)
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

	for i, t := range self.Tracks {
		if !t.Valid(false) {
			self.Tracks[i] = nil
		}
	}
	for k, c := range self.Collections {
		if !c.Valid(false) {
			delete(self.Collections, k)
		}
	}
}

func (self *Library) getTrack(id string) *Track {
	self.Lock.RLock()
	index, ok := self._track_map[id]
	if ok {
		track := self.Tracks[index]
		if track != nil {
			if !util.Contains_a_fucking_string(track.IDs, id) {
				panic("uh oh")
			}
			self.Lock.RUnlock()
			return track
		}
	}

	for index, track := range self.Tracks {
		if util.Contains_a_fucking_string(track.IDs, id) {
			self.Lock.RUnlock()
			self.Lock.Lock()
			self._track_map[id] = index
			self.Lock.Unlock()
			return track
		}
	}
	self.Lock.RUnlock()
	return nil
}

func (self *Library) getTracks(id string, filter *Filter) []*Track {
	//no lock because it would deadlock when getTrack locks
	self.Lock.RLock()
	c, ok := self.Collections[id]
	self.Lock.RUnlock()

	if ok {
		if c.Ignored {
			return nil
		}
		if filter != nil && !filter.PassCollection(*c) {
			return nil
		}
		tl := make([]*Track, 0, len(c.TracksIDs))
		for _, t := range c.TracksIDs {
			/* No good way to pass on filter
			tt := self.getTracks(t, nil)
			for _, v := range tt {
				tl = append(tl, v)
			}
			*/
			if t == "" {
				fmt.Printf("error with collection %s\n", c.ID)
				return []*Track{}
			}

			tt := self.getTrack(t)
			if tt == nil {
				fmt.Printf("error with collection %s track %s\n", c.ID, t)
				return []*Track{}
			}
			tl = append(tl, tt)
		}
		return tl
	} else {
		t := self.getTrack(id)
		if t != nil {
			if filter != nil && !filter.PassTrack(*t) {
				return nil
			}
			return []*Track{t}
		} else {
			return nil
		}
	}
}

func (self *Library) addTrack(track Track) (*Track, bool) {
	//it is a bug for a track to not have any IDs
	t0 := self.getTrack(track.IDs[0])

	track.Index = -1
	lt := make([]*Track, 0, len(track.IDs)+1)
	for _, id := range track.IDs {
		t1 := self.getTrack(id)
		if t1 != nil {
			repeat := false
			for _, t := range lt {
				if t.Index == t1.Index {
					repeat = true
				}
			}
			if !repeat {
				lt = append(lt, t1)
			}
		}
	}

	if len(lt) > 0 {
		lt = append(lt, &track)
		self.mergeTracks(lt...)
	} else {
		self.Lock.Lock()
		track.Index = len(self.Tracks)
		self.Tracks = append(self.Tracks, &track)
		self.Lock.Unlock()
	}

	t := self.getTrack(track.IDs[0])
	if t0 != nil && reflect.DeepEqual(*t, *t0) /*XXX bugged*/ {
		return t, false
	}
	return t, true
}

func (self *Library) mergeTracks(tracks ...*Track) {
	self.Lock.Lock()
	for i := 1; i < len(tracks); i++ {
		tracks[0].Merge(*tracks[i])
		if tracks[i].Index != -1 {
			self.Tracks[tracks[i].Index] = nil
		}
	}
	self.Lock.Unlock()
}

func (self *Library) getCollection(id string) *Collection {
	self.Lock.RLock()
	c := self.Collections[id]
	self.Lock.RUnlock()
	return c
}

func (self *Library) addCollection(c Collection) (*Collection, bool) {
	self.Lock.Lock()
	p, ok := self.Collections[c.ID]
	if !ok || p.Service != c.Service || p.ID != c.ID {
		self.Collections[c.ID] = &c
	} else {
		if len(c.TracksIDs) > 0 {
			p.TracksIDs = c.TracksIDs
			p.Rev = c.Rev
			p.Date = c.Date
		}
		if c.Name != "" {
			p.Ignored = c.Ignored
			p.Name = c.Name
		}
		p.Type = c.Type
	}
	self.Lock.Unlock()
	ret := self.getCollection(c.ID)
	if ok && cmp.Equal(*ret, *p) /*XXX bugger*/ {
		return ret, false
	} else {
		return ret, true
	}
}

//Eventually this should be replaced with universe.
type Track struct {
	SpotifyInfo      Cached_SpotifyInfo
	SpotifyExtraInfo Cached_SpotifyExtraInfo

	YoutubeInfo Cached_YoutubeInfo

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

func (self Track) Valid(dopanic bool) bool {
	if len(self.IDs) == 0 || len(self.IDMaps) == 0 {
		b, _ := json.MarshalIndent(self, "", "\t")
		if dopanic {
			panic(string(b))
		} else {
			fmt.Printf("Bad Track : %s\n", string(b))
		}
		return false
	}
	return true
}

func (t1 *Track) Merge(t2 Track) {
	if t2.SpotifyInfo.Initialized {
		t1.SpotifyInfo = t2.SpotifyInfo
	}
	if t2.SpotifyExtraInfo.Initialized {
		t1.SpotifyExtraInfo = t2.SpotifyExtraInfo
	}
	if t2.YoutubeInfo.Initialized {
		t1.YoutubeInfo = t2.YoutubeInfo
	}

	for _, t2_id := range t2.IDs {
		if !util.Contains_a_fucking_string(t1.IDs, t2_id) {
			t1.IDs = append(t1.IDs, t2_id)
		}
	}
	for t2_service, t2_ii := range t2.IDMaps {
		ii := util.Fucking_index(t1.IDs, t2.IDs[t2_ii])
		if ii == -1 {
			panic("barg")
		}
		t1.IDMaps[t2_service] = ii
	}

	return
}

func (self *Track) GetId(service string) (string, bool) {
	i, ok := self.IDMaps[service]
	if ok {
		return self.IDs[i], ok
	} else {
		return "", ok
	}
}

func make_tracks(ids []string, service string) []Track {
	tracks := make([]Track, len(ids))
	for i, id := range ids {
		tracks[i] = make_track(id, service)
	}
	return tracks
}

func make_track(id, service string) Track {
	return Track{
		IDs:    []string{id},
		IDMaps: map[string]int{service: 0},
	}
}

type Collection struct {
	//This exists to map the IDs of album posts to the tracks they contain
	//Unlike Track, these are not unified across services.
	//Once again, we just hope there are no cross service ID conflicts
	ID        string
	TracksIDs []string //can be nil

	Service string    //collection are single service
	Type    string    //playlist or album
	Owner   string    //used by List_Playlist
	Size    int       //useful to pass length before we know the track list
	Ignored bool      //whether to ignore
	Rev     string    //revision, optional
	Date    time.Time //when we got the data
	Name    string    //for my sanity
}

func (self Collection) Valid(dopanic bool) bool {
	if self.ID == "" || self.Service == "" {
		b, _ := json.MarshalIndent(self, "", "\t")
		if dopanic {
			panic(string(b))
		} else {
			fmt.Printf("Bad Collection : %s\n", string(b))
		}
		return false
	}
	return true
}

type Filter struct {
	Playlist string
	Album    bool
	Track    bool
}

func (self Filter) PassCollection(c Collection) bool {
	if c.Type == "playlist" {
		if self.Playlist == "" {
			return false
		}
		if self.Playlist == "all" {
			return true
		}
		if self.Playlist != c.Name {
			return false
		}
	}
	if c.Type == "album" {
		return self.Album
	}
	return true
}
func (self Filter) PassTrack(t Track) bool {
	return self.Track
}

func (self Filter) PassEntry(e Entry) bool {
	if e.Type == "playlist" && self.Playlist == "" {
		return false
	}
	if e.Type == "album" && !self.Album {
		return false
	}
	if e.Type == "track" && !self.Track {
		return false
	}
	return true
}

var DefaultFilter = Filter{
	Playlist: "",
	Album:    true,
	Track:    true,
}

type Playlist struct {
	Name     string
	Channels []string //nil for all channels
	NoRepeat bool

	Shuffle int
	Reverse bool

	Filter *Filter

	Contributors []string

	tracks       []*Track
	last_rebuild time.Time
}

func (self *Playlist) Rebuild() bool {
	if self.Filter == nil {
		self.Filter = &DefaultFilter
	}

	accept := func(entry *Entry) bool {
		//channel matching
		if len(self.Channels) != 0 {
			if !util.Contains_a_fucking_string(self.Channels, entry.ChannelId) {
				return false
			}
		}

		//type
		if self.Filter != nil {
			return self.Filter.PassEntry(*entry)
		}
		return true
	}

	global.em.Lock.RLock()

	type pair struct {
		a []*Track
		b time.Time
	}

	el := make([]Entry, 0, len(global.em.Entries))
	for _, e := range global.em.Entries {
		if accept(&e) {
			el = append(el, e)
		}
	}

	sort.Slice(el, func(i, j int) bool {
		if self.Reverse {
			return el[i].Date.After(el[j].Date)
		} else {
			return el[i].Date.Before(el[j].Date)
		}
	})

	repeat_entry := make(map[string]bool)
	//repeat_track := make(map[string]bool) TODO trackwise no repeat

	tl := make([]*Track, 0, len(self.tracks))
	for _, e := range el {
		if self.NoRepeat {
			if repeat_entry[e.ID] {
				continue
			} else {
				repeat_entry[e.ID] = true
			}
		}
		tl = append(tl, global.lib.getTracks(e.ID, self.Filter)...)
	}

	if self.Shuffle != 0 {
		//use as seed
		rand.Seed(int64(self.Shuffle * len(tl)))
		rand.Shuffle(len(tl), func(i, j int) {
			tl[i], tl[j] = tl[j], tl[i]
		})
	}

	//did anything change?
	var ret bool
	if len(self.tracks) != len(tl) {
		ret = true
	} else {
		for i, t := range self.tracks {
			if t != tl[i] {
				ret = true
			}
		}
	}

	//check
	te := 0
	for _, v := range tl {
		if v == nil {
			te++
		} else if v.IDMaps == nil {
			te++ // should never happen
		}
	}
	if te != 0 {
		fmt.Printf("%d errors in playlist creation\n", te)
	}

	self.tracks = tl
	global.em.Lock.RUnlock()
	self.last_rebuild = time.Now()
	return ret
}
