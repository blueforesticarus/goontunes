package main

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/blueforesticarus/goontunes/util"
	"github.com/bwmarrin/discordgo"
	"mvdan.cc/xurls/v2"
)

func Snowflake(t time.Time) string {
	var timestamp uint64
	timestamp = uint64(t.UnixMilli()) - 1420070400000
	timestamp = (timestamp << 22)
	return strconv.FormatUint(timestamp, 10)
}

func Timestamp(snowflake string) *time.Time {
	ts, _ := discordgo.SnowflakeTimestamp(snowflake)
	/*if ts.Year() == 1 {
		return nil
	}*/
	return &ts
}

type DiscordApp struct {
	Token    string
	Channels []string
	dg       *discordgo.Session

	output util.Pipe
}

var rxRelaxed = xurls.Relaxed() //precompile

///XXX reconnect
func (self *DiscordApp) Connect() {
	// Create a new Discord session using the provided bot token.
	if self.Token == "" || self.Token == "<yours>" {
		log.Fatalf("DISCORD: Generate a token following the instructions here: %s\n", helpurl)
	}

	dg, err := discordgo.New("Bot " + self.Token)
	if err != nil {
		fmt.Println("DISCORD: error creating session with token ", self.Token, err)
		return
	}

	// This function will be called (due to AddHandler below) when the bot receives
	// the "ready" event from Discord.
	var ready sync.WaitGroup
	on_ready := func(s *discordgo.Session, event *discordgo.Ready) {
		fmt.Println("DISCORD: connected")
		if len(self.Channels) == 0 {
			self.Channels = self.scan_channels()
			if len(self.Channels) == 0 {
				log.Fatalf("DISCORD: Your discord bot is not in any channels. You need to invite the bot to your music channel.\n")
			}
		}

		self.check_channels()
		ready.Done()
	}

	self.dg = dg
	dg.AddHandler(self.on_message)
	dg.AddHandler(on_ready)

	dg.Identify.Intents = discordgo.IntentsGuildMessages

	// Open a websocket connection to Discord and begin listening.
	ready.Add(1)
	err = dg.Open()
	if err != nil {
		fmt.Println("DISCORD: error opening connection,", err)
		return
	}

	//XXX we assume here that if Open does not give error, then on_ready WILL be called
	fmt.Println("waiting for discord")
	ready.Wait()
	self.fetch_messages_all(true)

	fmt.Println("DISCORD: done")
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the authenticated bot has access to.
func (self *DiscordApp) on_message(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore all messages created by the bot itself
	// This isn't required in this specific example but it's a good practice.
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !util.Contains_a_fucking_string(self.Channels, m.ChannelID) {
		return
	}

	self.output.Set(1)
	defer self.output.Set(-1)
	self.process_message(m.Message)
}

func (self *DiscordApp) check_channels() {
	for i, cid := range self.Channels {
		ch, err := self.dg.Channel(cid)
		if err != nil {
			fmt.Printf("DISCORD: could not load channel %v\n", cid)
			self.Channels[i] = ""
		} else {
			fmt.Printf("DISCORD: channel %s -> %s\n", ch.ID, ch.Name)
		}
	}
}

func (self *DiscordApp) scan_channels() []string {
	// Loop through each guild in the session
	ids := make([]string, 0, 1)

	gs, err := self.dg.UserGuilds(100, "", "")
	if err != nil {
		fmt.Printf("Discord: cannot load guilds %v\n", err)
	}
	for _, guild := range gs {
		fmt.Printf("Discord: found guild %s\n", guild.Name)
		// Get channels for this guild
		channels, _ := self.dg.GuildChannels(guild.ID)

		for _, c := range channels {
			// Check if channel is a guild text channel and not a voice or DM channel
			if c.Type == discordgo.ChannelTypeGuildText {
				fmt.Printf("DISCORD: found %s -> %s\n", c.ID, c.Name)
				ids = append(ids, c.ID)
			}
		}
	}
	return ids
}

func (self *DiscordApp) fetch_messages_all(use_cache bool) {
	self.output.Set(1)
	defer self.output.Set(-1)

	st := time.Now()

	var start string
	for _, cid := range self.Channels {
		if cid != "" {
			if use_cache {
				start = global.Latest("discord", cid)
			} else {
				start = ""
			}
			//self.the_algorythm(cid, start)
			self.fetch_messages(start, cid)
		}
	}

	duration := time.Since(st)

	fmt.Printf("DISCORD: fetched messages in %v seconds\n", duration.Seconds())
}

var MaxMessage = 100

func (self *DiscordApp) fetch_messages(start string, ch string) {
	if start == "" {
		start = ch
	}

	st := Timestamp(start).Format("2006-01-02")
	ms, err := self.dg.ChannelMessages(ch, MaxMessage, "", start, "")
	if err != nil {
		fmt.Println("DISCORD: error loading history", err)
		return
	}

	for _, message := range ms {
		self.process_message(message)
	}

	if len(ms) > 0 {
		a, _ := ms[0].Timestamp.Parse()
		b, _ := ms[len(ms)-1].Timestamp.Parse()
		fmt.Printf("DISCORD: %s fetched chunk %s - %s\n", ch, st, a.Format("2006-01-02"))

		if len(ms) >= MaxMessage {
			if !a.After(b) {
				panic("bad message order 2")
			}

			self.fetch_messages(ms[0].ID, ch)
		}
	}
}

func (self *DiscordApp) process_message(message *discordgo.Message) {
	if message.Type != discordgo.MessageTypeDefault {
		return
	}

	ts, _ := message.Timestamp.Parse()

	template := Entry{
		Platform:   "discord",
		ChannelId:  message.ChannelID,
		PosterId:   message.Author.ID,
		PosterName: message.Author.Username,
		MessageId:  message.ID,
		Date:       ts,
	}

	ls := rxRelaxed.FindAllString(message.Content, -1)
	for _, s := range ls {

		entry := template
		entry.Url = s
		process_entry(&entry)

		if entry.Valid {
			self.output.Plumb(entry)
		}
	}
}

/* This could argueably be moved to a seperate file since it is used by p_element */
func process_entry(entry *Entry) {
	try_spotify(entry)
	if !entry.Valid {
		try_youtube(entry)
	}
	if !entry.Valid {
		try_soundcloud(entry)
	}
	//TODO support apple music, apparently people actually use apple music in some countries
}
