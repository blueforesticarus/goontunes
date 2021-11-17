package main

import "github.com/blueforesticarus/goontunes/util"

type API interface {
	Connect()
}

//URL Sources
type Latest = func(string, string) string
type Platform interface {
	API
	Fetch_Messages(bool)
	Init(util.Input, Latest)
}

//Playlist Output
type Service interface {
	API
	Update_Playlist(Playlist)
}

//I have no idea how to implement automatic reconnect
