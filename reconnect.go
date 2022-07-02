package main

/* unfinished refactor of connect logic*/

import "github.com/blueforesticarus/goontunes/util"

type App interface {
	Connect()
}

//URL Sources
type Latest = func(string, string) string
type Platform interface {
	App
	Fetch_Messages(bool)
	Init(util.Input, Latest)
}

//Playlist Output
type ServiceApp interface {
	App
}

//I have no idea how to implement automatic reconnect
