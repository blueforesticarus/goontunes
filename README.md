# GoonTunes
### spotify playlists for the you and your goons

Epic Features:
- Create Spotify Playlists from Discord Posts
- Merge you and your friends discover weekly into a single playlist
    - just post your DW playlist into the Discord channel the bot watches
- fully automatic, as long as it doesnt crash

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
- rewrite all of this shitty ass code

