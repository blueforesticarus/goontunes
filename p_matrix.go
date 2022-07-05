package main

import (
	"github.com/blueforesticarus/goontunes/util"
	"github.com/bwmarrin/discordgo"
)

type MatrixApp struct {
	Token    string
	Channels []string
	dg       *discordgo.Session

	output util.Pipe
}

func (self *MatrixApp) IsNil() bool {
	return self == nil
}

///XXX reconnect
func (self *MatrixApp) Connect() {
	//TODO
}

