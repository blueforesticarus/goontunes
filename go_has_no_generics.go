package main

import (
	"strconv"
	"time"
)

//really feeling that lack of generics
func ToCollections(ls ...interface{}) []Collection {
	ret := make([]Collection, len(ls))
	for i, v := range ls {
		ret[i] = v.(Collection)
	}
	return ret
}

func ToTracks(ls ...interface{}) []Track {
	ret := make([]Track, len(ls))
	for i, v := range ls {
		ret[i] = v.(Track)
	}
	return ret
}

func ToEntries(ls ...interface{}) []Entry {
	ret := make([]Entry, len(ls))
	for i, v := range ls {
		ret[i] = v.(Entry)
	}
	return ret
}

func ToStrings(ls ...interface{}) []string {
	ret := make([]string, len(ls))
	for i, v := range ls {
		ret[i] = v.(string)
	}
	return ret
}

//more fun :)
func IDCollections(ls interface{}) string {
	return ls.(Collection).ID
}

func IDTracks(ls interface{}) string {
	return strconv.Itoa(ls.(Track).Index)
}

func IDEntries(ls interface{}) string {
	return ls.(Entry).MessageId
}

func IDString(ls interface{}) string {
	return ls.(string)
}

type Cached_SpotifyExtraInfo struct {
	Initialized bool
	V           *SpotifyExtraInfo
	Date        time.Time
	Failed      int
}
type Cached_SpotifyInfo struct {
	Initialized bool
	V           *SpotifyInfo
	Date        time.Time
	Failed      int
}
type Cached_YoutubeInfo struct {
	Initialized bool
	V           *YoutubeInfo
	Date        time.Time
	Failed      int
}
