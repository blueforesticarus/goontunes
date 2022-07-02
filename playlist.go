package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"github.com/blueforesticarus/goontunes/util"
	"github.com/lytics/base62"
	"github.com/pmezard/go-difflib/difflib"
)

type Pl_Rm struct {
	id string
	i  int
}

type Pl_Ins struct {
	ids []string
	i   int
}

type PlaylistService interface {
	Name() string
	Fetch_Playlist(string) *Collection
	Fetch_Playlist_Tracks(Collection) *Collection
	Get_Track_Id(*Track) string
	Playlist_InsertTracks(string, []Pl_Ins) int
	Playlist_DeleteTracks(string, []Pl_Rm) int
	Create_Playlist(string) string
	List_Playlists() []Collection
}

func (self *ServicePlaylist) Save() {
	bytes, _ := json.MarshalIndent(self.cache, "", "")
	_ = ioutil.WriteFile(self.cachepath(), bytes, 0644)
}

func (self *ServicePlaylist) Load() {
	bytes, err := ioutil.ReadFile(self.cachepath())
	if err != nil {
		return
	}

	err = json.Unmarshal([]byte(bytes), &self.cache)
}

func (self *ServicePlaylist) cachepath() string {
	return fmt.Sprintf("%s/%s.%s.pc", global.CachePath, self.service.Name(), self.ID)
}

func (self *ServicePlaylist) idcache() string {
	return global.CachePath + "/" + base62.StdEncoding.EncodeToString([]byte(self.Name))
}

func (self *ServicePlaylist) Init(service PlaylistService) {
	self.service = service

	var v *Collection = nil
	if self.ID == "" {
		if self.Name == "" {
			self.ignore = true
			return
		}

		bytes, _ := os.ReadFile(self.idcache())
		self.ID = string(bytes)
		//TODO at least with spotify, we should use the simpleplaylist returned when getting the list of user playlists
		v = self.service.Fetch_Playlist(self.ID)

		if v == nil {
			userplaylists := self.service.List_Playlists()
			for _, pl := range userplaylists {
				if pl.Name == self.Name {
					self.ID = pl.ID
				}
			}

			if self.ID == "" {
				self.ID = self.service.Create_Playlist(self.Name)
			}
			os.WriteFile(self.idcache(), []byte(self.ID), 0644)
		}
	}

	if v == nil {
		v = self.service.Fetch_Playlist(self.ID)
	}

	if v == nil {
		fmt.Printf("%s: configured playlist %s:%s unavailable\n", self.service.Name(), self.ID, self.Name)
	} else {
		fmt.Printf("%s: configured playlist %s found \"%s\"\n", self.service.Name(), self.ID, self.Name)
		self.Load()

		if self.cache == nil || self.cache.Rev != v.Rev || self.cache.Rev == "" {
			c := self.service.Fetch_Playlist_Tracks(*v)
			if c != nil {
				self.cache = c
				self.Save()
			}
		}
	}
}

type ServicePlaylist struct {
	ID   string
	Name string

	Sync       string //the internal playlist to sync to
	NoDelete   bool
	NoCrossRef bool

	ignore  bool
	service PlaylistService
	cache   *Collection
}

func (self *ServicePlaylist) scan() error {
	if self.ID == "" {
		return fmt.Errorf("Playlist %s has no known ID", self.Name)
	}

	//get metadata
	v := self.service.Fetch_Playlist(self.ID)
	if v == nil {
		return fmt.Errorf("configured playlist %s unavailable", self.ID)
	}

	//update cache of playlist tracks
	if self.cache == nil || v.Rev != self.cache.Rev {
		c := self.service.Fetch_Playlist_Tracks(*v)
		if c == nil {
			return fmt.Errorf("could not update playlist cache")
		}
		self.cache = c
		self.Save()
	}
	return nil
}

func (self *ServicePlaylist) check(target []string) error {
	if self.cache == nil || len(self.cache.TracksIDs) != len(target) {
		return fmt.Errorf("error detected in playlist update\n")
	}
	for i, v := range self.cache.TracksIDs {
		if v != target[i] {
			return fmt.Errorf("error detected in playlist update\n")
		}
	}
	return nil
}

//higher level playlist update function
func (self *ServicePlaylist) Update(p *Playlist) error {
	fmt.Printf("Begin sync of %s <-> %s\n", self.ID, p.Name)

	//assume p is correct playlist, and p.rebuild called
	err := self.scan()
	if err != nil {
		return fmt.Errorf("Abort playlist update. %v \n", err)
	}

	if len(p.tracks) == 0 {
		return fmt.Errorf("Abort playlist update. %v \n", "refusing to sync a empty playlist")
	}

	current := make([]string, len(self.cache.TracksIDs))
	for i, v := range self.cache.TracksIDs {
		current[i] = v
	}

	//get just the strings, filter things missing spot info
	target := make([]string, 0, len(p.tracks))
	for i, v := range p.tracks {
		if v == nil || v.IDMaps == nil {
			//bug
			return fmt.Errorf("Abort playlist sync, error with track")
		}

		id := self.service.Get_Track_Id(v)
		if id != "" {
			//no id for this track on this service
			continue
		}
		if self.NoCrossRef {
			panic("unimplemented")
			//NOTE: not sure clean way to add this functionality
			//if the id from the entry is not the same, then it is a cross referenced track
		}

		if self.NoDelete {
			if util.Contains_a_fucking_string(target, id) ||
				util.Contains_a_fucking_string(current, id) {
				// we skip duplicates for nodelete, kinda have to
				continue
			}
		}
		target[i] = id
	}

	if self.NoDelete {
		//for nodelete we target the playlist plus current
		//NOTE: this is prepending, new stuff will be at the top
		target = append(target, current...)
	}

	fmt.Printf("Attempting to sync %d/%d tracks\n", len(target), len(p.tracks))

	//compute delta
	rm_list := make([]Pl_Rm, 0, 100)
	ins_list := make([]Pl_Ins, 0, 100)

	sm := difflib.NewMatcher(current, target)
	for _, v := range sm.GetOpCodes() {
		if v.Tag == 'd' || v.Tag == 'r' {
			for i := v.I1; i < v.I2; i++ {
				a := Pl_Rm{current[i], i}
				rm_list = append(rm_list, a)
			}
		}

		if v.Tag == 'i' || v.Tag == 'r' {
			b := Pl_Ins{target[v.J1:v.J2], v.J1}
			ins_list = append(ins_list, b)
		}
	}

	//sort strings (don't rely on snapshot)
	sort.Slice(ins_list, func(i, j int) bool {
		return ins_list[i].i < ins_list[j].i //insert last first
	})

	sort.Slice(rm_list, func(i, j int) bool {
		return rm_list[i].i > rm_list[j].i //sort reverse
	})

	if len(ins_list) == 0 && len(rm_list) == 0 {
		fmt.Printf("playlist is already correct\n")
	} else {
		//delete tracks
		if len(rm_list) > 0 {
			n := self.service.Playlist_DeleteTracks(self.ID, rm_list)
			fmt.Printf("updated playlist %s, %d deleted\n", self.ID, n)
		}
		if len(ins_list) > 0 {
			n := self.service.Playlist_InsertTracks(self.ID, ins_list)
			fmt.Printf("SPOTIFY: updated playlist %s, %d inserted\n", self.ID, n)
		}
	}

	err = self.scan()
	if err == nil {
		err = self.check(target)
	}
	return err
}

type FileOutput struct {
	ServicePlaylist
}
