package main

import (
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	output   util.Input
}

/*
func (self *DiscordApp) clone() *DiscordApp {
	var c DiscordApp
	c.Token = self.Token

	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + self.Token)
	if err != nil {
		fmt.Println("DISCORD: error creating session with token ", self.Token, err)
	}

	c.dg = dg
	dg.Identify.Intents = discordgo.IntentsGuildMessages

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("DISCORD: error opening connection,", err)
	}

	return &c
}
*/
var rxRelaxed = xurls.Relaxed() //precompile

func (self *DiscordApp) connect() {
	// Create a new Discord session using the provided bot token.
	if self.Token == "" || self.Token == "<yours>" {
		log.Fatalf("DISCORD: Generate a token following the instructions here: %s\n", helpurl)
	}

	dg, err := discordgo.New("Bot " + self.Token)
	if err != nil {
		fmt.Println("DISCORD: error creating session with token ", self.Token, err)
		return
	}

	self.dg = dg
	dg.AddHandler(self.on_message)
	dg.AddHandler(self.on_ready)

	dg.Identify.Intents = discordgo.IntentsGuildMessages

	// Open a websocket connection to Discord and begin listening.
	err = dg.Open()
	if err != nil {
		fmt.Println("DISCORD: error opening connection,", err)
		return
	}
}

func (self *DiscordApp) close() {
	self.dg.Close()
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

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the authenticated bot has access to.
func (self *DiscordApp) on_message(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore all messages created by the bot itself
	// This isn't required in this specific example but it's a good practice.
	if m.Author.ID == s.State.User.ID {
		return
	}

	if !contains_a_fucking_string(self.Channels, m.ChannelID) {
		return
	}

	self.output.Set(1)
	defer self.output.Set(-1)
	self.process_message(m.Message)
}

// This function will be called (due to AddHandler above) when the bot receives
// the "ready" event from Discord.
func (self *DiscordApp) on_ready(s *discordgo.Session, event *discordgo.Ready) {
	fmt.Println("DISCORD: connected")

	if len(self.Channels) == 0 {
		self.Channels = self.scan_channels()
		if len(self.Channels) == 0 {
			log.Fatalf("DISCORD: Your discord bot is not in any channels. You need to invite the bot to your music channel.\n")
		}
	}

	self.check_channels()

	self.output.Set(1)
	defer self.output.Set(-1)
	self.fetch_messages_all(true)
	global.plumber.rescan()
}

func (self *DiscordApp) fetch_messages_all(use_cache bool) {
	self.output.Set(1)
	defer self.output.Set(-1)

	st := time.Now()

	var start string
	for _, cid := range self.Channels {
		if cid != "" {
			if use_cache {
				start = global.em.Latest("discord", cid).MessageId
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
		process_entry(entry, self.output)
	}
}

func process_entry(entry Entry, output util.Input) {
	try_spotify(&entry)
	if !entry.Valid {
		try_youtube(&entry)
	}
	if !entry.Valid {
		try_soundcloud(&entry)
	}

	if entry.Valid {
		output.Plumb(entry)
	}
}

func try_spotify(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}
	if strings.Contains(u.Host, "spotify") {
		entry.Service = "spotify"

		entry.Valid = true
		if strings.Contains(entry.Url, "album") {
			entry.Type = "album"
			entry.IsTrack = false
		} else if strings.Contains(entry.Url, "track") {
			entry.Type = "track"
			entry.IsTrack = true
		} else if strings.Contains(entry.Url, "user") {
			entry.Type = "user"
			entry.IsTrack = false
		} else if strings.Contains(entry.Url, "playlist") {
			entry.Type = "playlist"
			entry.IsTrack = false
		} else {
			entry.Valid = false
			return
		}

		re := regexp.MustCompile("/" + entry.Type + "/([0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz]+)")
		ms := re.FindStringSubmatch(u.Path)
		if len(ms) == 2 {
			entry.ID = ms[1]
		} else {
			entry.Valid = false
		}
	}

}

/*
TODO unit test
https://www.youtube.com/watch?v=0zM3nApSvMg&feature=feedrec_grec_index
https://www.youtube.com/user/IngridMichaelsonVEVO#p/a/u/1/QdK8U-VIH_o
https://www.youtube.com/v/0zM3nApSvMg?fs=1&amp;hl=en_US&amp;rel=0
https://www.youtube.com/watch?v=0zM3nApSvMg#t=0m10s
https://www.youtube.com/embed/0zM3nApSvMg?rel=0
https://www.youtube.com/watch?v=0zM3nApSvMg
https://youtu.be/0zM3nApSvMg
*/

func try_youtube(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}
	if strings.Contains(u.Host, "youtube") || strings.Contains(u.Host, "youtu.be") {
		entry.Service = "youtube"
		re := regexp.MustCompile(`(?:[?&]v=|\/embed\/|\/1\/|\/v\/|https:\/\/(?:www\.)?youtu\.be\/)([^&\n?#]+)`)
		ms := re.FindStringSubmatch(entry.Url)
		if len(ms) == 2 {
			entry.ID = ms[1]
			entry.Valid = true
			entry.IsTrack = true
		} else {
			entry.Valid = false
		}
	}
}

func try_soundcloud(entry *Entry) {
	u, err := url.Parse(entry.Url)
	if err != nil {
		println("invalid url")
		return
	}

	if strings.Contains(u.Host, "soundcloud") {
		entry.Service = "soundcloud"
		entry.IsTrack = true
		entry.ID = u.Path //XXX RawPath?
		entry.Valid = true
	}
}
