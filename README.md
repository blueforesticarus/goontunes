# GoonTunes
### spotify playlists for the you and your goons

Epic Features:
- Create Spotify Playlists from Discord Posts
- Manage a playlist to archive your Discover Weekly
- Merge you and your friends discover weekly into a single playlist
    - just post your DW playlist into the Discord channel the bot watches
- fully automatic, ~~as long as it doesnt crash~~ now with correct™ behavior
    - Listens for new posts and input playlist changes

## Setup
1. Create a discord bot token
2. Add the bot to your discord music channel(s)
3. Create a spotify dev app 
    - you will need clientsecret, clientid, and redirect_uri
    - redirect must be of the form "https://localhost:\<port\>"
3. Copy config/minimal.yaml to .config and fill in your values

4. build with go build
5. run with ./goontunes or install the bob.service 

## TODO
- enforce id semantics, retry on bad id
- youtube playlist support
- soundcloud playlist support
- youtube/spotify crosslist support
- playlist file output
- file input
- matrix/element support
- cli
- post to channel by adding to playlist
- *secret feature*

- rewrite with database
- rewrite with elixir
