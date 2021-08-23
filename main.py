class Entry():
    def __init__(self,message, url):        
        self.poster = str(message.author)
        self.date = message.created_at
        self.id = message.id
        self.parse(url)

    def parse(self, url):
        if "/album/" in url:
            self.type = "album"
        elif "/track/" in url:
            self.type = "track"
        else:
            print("ignoring %s" % url)
            raise ValueError

        # need to extract the track id
        # ex. https://open.spotify.com/track/0ZxWG91nxZcCsEUd6ykc6i?si=575d58d2835a478a
        import re
        uid = re.search(r"spotify.com/" + self.type + "/([^?]*)?" , url).group(1)
        self.uri = "spotify:{}:{}".format(self.type, uid)

class YoutubeEntry(Entry):
    def parse(self, url):
        import re
        pattern = r"^.*(?:youtube\.com|youtu\.be)/(?:watch\?v\=)?([^\?]*)"
        match = re.search(pattern, url)
        if match:
            self.url = url
            self.uri = match.group(1)
        else:
            print("ignoring %s" % url)
            raise ValueError

        self.has_match = False

    def fetch_info(self):
        from ytmusicapi import YTMusic
        ytmusic = YTMusic()
        try:
            data = ytmusic.get_song(self.uri)
        except:
            import logging
            logging.exception('')
            return False

        try:
            data = data['microformat']['microformatDataRenderer']
        except:
            data = None

        if data and data['category'] == "Music":
            self.title = data['title']
            self.title = self.title.replace("- YouTube Music", "")
            self.title = self.title.replace("(Official Video)", "")

            from spotify import sp
            try:
                data = sp.search(self.title)
                items = data['tracks']['items']
                def same_artist():
                    from collections import defaultdict
                    ars = defaultdict(lambda : 0)
                    for item in items:
                        for a in item['artists']:
                            ars[a['uri']] += 1
                    
                    al = []
                    for a in items[0]['artists']:
                        al.append(a['uri'])

                    artist,n = max(ars.items(), key = lambda x:x[1])
                    return (n > len(items)/2) and (artist in al)
                
                if len(items) > 1 and not same_artist():
                    print('multible spotify results from diff artists for "%s" - skipping' % self.title)
                elif len(items) == 0:
                    print('no spotify results for "%s" - skipping' % self.title )
                else:
                    self.spotify_uri = items[0]['uri']
                    self.has_match = True
            except:
                import logging
                logging.exception('')
        else:
            print("skipping non-music YT %s" % self.url)

        return self.has_match

class YoutubeConvertedEntry(Entry):
    def __init__(self, e):
        assert e.has_match
        self.id = e.id
        self.date = e.date
        self.poster = e.poster

        self.parse(uri_to_link(e.spotify_uri))

class EntryManager():
    def __init__(self, library):
        self.library = library
        self.entries = [] # index 0 is oldest
        self.ids = {}
        self.last_sync = None
        self.youtube = {}
        self.yt_blacklist = []

    def add(self, entry):
        if entry.id not in self.ids:
            self.ids[entry.id] = len(self.entries)
            self.entries.append(entry)
            if self.last_sync is None or self.last_sync < entry.date:
               self.last_sync = entry.date
            self.sync_entry(entry) 
            return True
        else:
            #print("skipping known msg:%s %s" % (entry.id, entry.uri))
            return False



    def youtube_add(self, entry):
        if entry.id not in self.youtube:
            self.youtube[entry.id] = entry
            return True
        else:
            return False

    def youtube_fetch(self):
        s = f = 0
        for e in self.youtube.values():
            if not e.has_match and not e.url in self.yt_blacklist:
                if not e.fetch_info():
                    f += 1
                    self.yt_blacklist.append(e.url)
                else:
                    s += 1
        print("determined spotify uri for {} YT links, failed for {}".format(s,f))

    def youtube_convert(self):
        N = 0
        for pid, e in self.youtube.items():
            if e.has_match and pid not in self.ids:
                N += self.add(YoutubeConvertedEntry(e))
        if N:
            self.sort_entries()

        return N

    def sort_entries(self):
        if self.entries:
            self.entries.sort(key = lambda e : e.date)
            self.ids={e.id : i for i,e in enumerate(self.entries)}
            self.last_sync = self.entries[-1].date

    def sync_entry(self, entry):
        if entry.type == 'track':
            track = self.library.tracks.add(entry.uri)
            track.posts.add(entry.id, entry)
        else:
            album = self.library.albums.add(entry.uri)
            if album.has_info:
                for t in album.tracks:
                    track = library.tracks.add(t)
                    track.posts.add(entry.id, entry)
            else:
                print("not syncing post %s of album %s b/c no metadata" % (entry.id,entry.uri))

    def sync_all(self):
        for e in self.entries:
            self.sync_entry(e)

    def save(self):
        self.sync_all()
        import pickle
        with open("not.music", 'w') as f:
            f.write("\n".join(self.yt_blacklist) + '\n')
        with open("data.pkl", 'wb') as f:
            pickle.dump(self,f)
        print("saved {} entries, [{} youtube]".format(len(self.entries), len(self.youtube)))
        self.library.save()
    
    def restore(self):
        import os
        if os.path.exists("not.music"):
            self.yt_blacklist = []
            with open("not.music",'r') as f:
                for line in f.readlines():
                    if line.strip():
                        self.yt_blacklist.append(line.strip())

        if os.path.exists("data.pkl"):
            import pickle
            with open("data.pkl",'rb') as f:
                data = pickle.load(f)
            self.entries = data.entries
            self.last_sync = data.last_sync
            self.ids = data.ids
            self.youtube = data.youtube
            self.sort_entries()
            import sys
            if "--full" in sys.argv:
                self.last_sync = None
            print("loaded {} entries [{} youtube]".format(len(self.entries), len(self.youtube)))

        ps = set()
        for e in self.entries:
            ps.add(e.poster)

        #print("\n".join(sorted(list(ps))))

        self.library.restore()
        self.sync_all()

    def __getstate__(self):
        state = self.__dict__.copy()
        # Don't pickle baz
        del state["library"]
        return state

    def track_list(self, no_dupes = False):
        L = []

        def add(item):
            if no_dupes and item in L:
                return
            L.append(item)

        for e in reversed(self.entries):
            if e.type == "album":
                album = self.library.albums[e.uri]
                assert album.has_info
                for t in album.tracks:
                    L.append(t)
            else:
                L.append(e.uri)
        
        return L

class FactoryDict(dict):
    def __init__(self, cls):
        self.cls = cls
    def add(self, uid, *args):
        if uid not in self:
            self[uid] = self.cls(uid, *args)
        return self[uid]

def uri_to_link(uri):
    return "http://open.spotify.com/{}/{}".format(uri.split(':')[-2],uri.split(":")[-1])

class Library():
    def __init__(self):
        self.tracks = FactoryDict(Track)
        self.artists = FactoryDict(Artist)
        self.albums = FactoryDict(Album)

        self.cache = {}
        self.yt_failed = []
    
    def save(self):
        import pickle
        with open("libr.pkl", 'wb') as f:
            pickle.dump(self,f)
        with open("yt.failed", 'w') as f:
            for uri in self.yt_failed:
                s = "{} {}\n".format(uri,self.cache_file_name(uri))
                f.write(s)
        print("saved {}:tracks {}:albums {}:artists | {} missing metadata".format(len(self.tracks),len(self.albums),len(self.artists), self.count_missing()))
    
    def restore(self):
        import os
        if not os.path.exists("libr.pkl"):
            return

        import pickle
        with open("libr.pkl",'rb') as f:
            data = pickle.load(f)
        self.albums  = data.albums
        self.artists = data.artists
        self.tracks  = data.tracks
        if hasattr(data, "cache"):
            self.cache = data.cache
        if os.path.exists("yt.failed"):
            self.yt_failed = []
            with open("yt.failed",'r') as f:
                for line in f.readlines():
                    uri = line.split(' ')[0]
                    self.yt_failed.append(uri)

        print("loaded {}:tracks {}:albums {}:artists | {} missing metadata".format(len(self.tracks),len(self.albums),len(self.artists), self.count_missing()))
        self.check_cache_all()

    def count_missing(self):
        import itertools
        i = 0
        for a,v in itertools.chain(
            self.albums.items(),
            self.tracks.items(),
            self.artists.items()
        ):
            if not v.has_info:
                i += 1
                print("missing metadata %s" % a)
        return i

    def playlist(self):
        return [ uri for uri, track in self.tracks.items() if ( track.posts and track.has_info )]

    async def download(self):
        self.check_cache_all()

        query = []
        paths = {}
        for uri in self.playlist():
            if not self.check_cache(uri) and not uri in self.yt_failed:
                query.append(uri)
                paths[uri] = self.cache_file_name(uri)

        for batch in batched(query, 10):
            links = [uri_to_link(uri) for uri in batch]

            async def dl(link):
                proc = await asyncio.create_subprocess_exec(
                        *(["spotdl", "-o", "yt.cache", link]),
                        stdout = asyncio.subprocess.PIPE,
                        stderr = asyncio.subprocess.PIPE,
                )
                stdout, stderr = await proc.communicate()

            await asyncio.gather(*[dl(link) for link in links])

            for uri in batch:
                if not self.check_cache(uri):
                    print("spotdl couldn't find %s" % uri)
                    self.yt_failed.append(uri)

            self.save()

        if query:
            self.check_cache_all()

    def check_cache(self,uri):
        import os
        if uri in self.cache:
            if not os.path.exists(self.cache[uri]):
                print("{} -> {} missing from cache".format(uri, self.cache[uri]))
                del self.cache[uri]
        elif self.tracks[uri].has_info:
            def try_path(path):
                if os.path.exists(path):
                    print("found in cache {} -> {} ".format(uri, path))
                    self.cache[uri] = path
                    return True

            guess1 = self.cache_file_name(uri)
            guess2 = self.cache_file_name(uri, True)
            guess3 = "yt.cache/{}.mp3".format(uri)

            try_path(guess1) or try_path(guess2) or try_path(guess3)

        return uri in self.cache

    def check_cache_all(self):
        n = m = q = 0
        for uri, t in self.tracks.items():
            if t.posts:
                n += 1
                if not self.check_cache(uri):
                    if uri in self.yt_failed:
                        m += 1
                    else:
                        q += 1

            elif uri in self.cache:
                # only cache if someone posted
                del self.cache[uri]

        print("{} / {} downloaded | {} failed | {} queued".format(len(self.cache), n, m, q))

    def cache_file_name(self, uri, hack=False):
            song_artists = []
            for a in self.tracks[uri].artists:
                song_artists.append(self.artists[a].name)

            song_name = self.tracks[uri].name

            # build file name of converted file
            # the main artist is always included
            artist_string = song_artists[0]

            # ! we eliminate contributing artist names that are also in the song name, else we
            # ! would end up with things like 'Jetta, Mastubs - I'd love to change the world
            # ! (Mastubs REMIX).mp3' which is kinda an odd file name.
            for artist in song_artists[1:]:
                if artist.lower() not in song_name.lower() or hack:
                    artist_string += ", " + artist

            converted_file_name = artist_string + " - " + song_name

            # ! this is windows specific (disallowed chars)
            converted_file_name = "".join(
                char for char in converted_file_name if char not in "/?\\*|<>"
            )

            # ! double quotes (") and semi-colons (:) are also disallowed characters but we would
            # ! like to retain their equivalents, so they aren't removed in the prior loop
            converted_file_name = converted_file_name.replace('"', "'").replace(":", "-")

            return "yt.cache/" + converted_file_name + ".mp3"

class Track():
    def __init__(self, uid):
        assert 'track' in uid
        self.id = uid
        self.posts = FactoryDict(PostRef) #id : PostRef
        self.has_info = False

    def add_info(self, name, album, artists, length):
        self.has_info = True
        self.album   = album #i believe in spotify a track has exactly one album
        self.artists = artists
        self.length  = length
        self.name    = name

class PostRef():
    def __init__(self, uid, entry):
        assert uid is entry.id
        self.id = entry.id
        self.poster = entry.poster
        self.date = entry.date

class Artist():
    def __init__(self, uid):
        assert 'artist' in uid
        self.id = uid
        self.has_info = False

    def add_info(self, name):
        self.has_info = True
        self.name = name

class Album():
    def __init__(self, uid):
        assert 'album' in uid
        self.id = uid
        self.has_info = False

    def add_info(self, name, tracks):
        self.has_info = True
        self.name = name
        self.tracks = tracks

library = Library()
entry_manager = EntryManager(library)

entry_manager.restore()

import radio
#radio = radio.Radio(library)
#radio.test()

import discord, asyncio
client = discord.Client()

async def messages():
    n = 0
    n_s = n_sn = 0
    n_y = n_yn = 0
    channel = client.get_channel(env.channel)
    async for message in channel.history(oldest_first=True, limit=30000, after=entry_manager.last_sync):
        s,sn,y,yn = process_message(message)
        n_s  += s
        n_sn += sn
        n_y  += y
        n_yn += yn
        n += 1
        if sn and (n_sn+1) % 50 == 0:
            #periodic saving while parsing whole tree
            entry_manager.save()
    
    print("processed {} posts, containing {} spotify links [{} new] | {} YT links [{} new]"
        .format(n,n_s,n_sn,n_y,n_yn)
    )

    return n_sn, n_yn


import re
URL_PATTERN = re.compile(r"""(?i)\b((?:https?:(?:/{1,3}|[a-z0-9%])|[a-z0-9.\-]+[.](?:[a-z]+)/)(?:[^\s()<>{}\[\]]+|\([^\s()]*?\([^\s()]+\)[^\s()]*?\)|\([^\s]+?\))+(?:\([^\s()]*?\([^\s()]+\)[^\s()]*?\)|\([^\s]+?\)|[^\s`!()\[\]{};:'".,<>?«»“”‘’])|(?:(?<!@)[a-z0-9]+(?:[.\-][a-z0-9]+)*[.](?:[a-z]+)\b/?(?!@)))""")

def process_message(message):
    if message.type != discord.MessageType.default:
        return 0,0,0,0

    s = y =0 #processed
    sn = yn = 0 #new

    urls = []
    for e in message.embeds:
        if e.type in ("link", "video"):
            urls.append(e.url)

    if not urls:
        urls = re.findall(URL_PATTERN, message.content)

    for url in urls:
        try:
            if "spotify.com" in url:
                entry = Entry(message,url)
                if entry_manager.add(entry):
                    sn += 1
                s += 1
            elif "youtube.com" in url or "youtu.be" in url:
                entry = YoutubeEntry(message,url)
                if entry_manager.youtube_add(entry):
                    yn += 1
                y += 1
        except ValueError:
            pass
        except:
            import logging
            logging.exception('parsing discord')

    return s, sn, y, yn

def list_channels():
    for c in client.get_all_channels():
        print(c.guild, c, c.id)

import env

@client.event
async def on_ready():
    print('Logged in as {0.user}'.format(client))
    if not env.channel:
        list_channels()
        print("ERR: copy the token from the room you want to env.py")
        await client.close()
    else:
        await messages()

        print("\nYOUTUBE\n")
        entry_manager.youtube_fetch()
        entry_manager.youtube_convert()
        entry_manager.save()
        
        print("\nSPOTIFY\n")
        spotify.fetch_needed(entry_manager.library)
        playlist_manager.update(entry_manager.track_list())
        entry_manager.save()

        print("\nDOWNLOAD\n")

        await entry_manager.library.download()
        entry_manager.save()

        print("\nSTARTUP COMPLETE\n")

@client.event
async def on_message(message):
    if message.channel.id == env.channel:
        #sleep makes sure the spotify embed is available when we fetch the message
        await asyncio.sleep(1)
        ns, ny = await messages()
        nys = None
        if ny:
            entry_manager.youtube_fetch()
            nys = entry_manager.youtube_convert()
        if ns or nys:
            spotify.fetch_needed(entry_manager.library)
            entry_manager.save()
            playlist_manager.update(entry_manager.track_list())
            await entry_manager.library.download()

import spotify
from spotify import batched
playlist_manager = spotify.PlaylistManager()

if env.token: 
    client.run(env.token)
else:
    print("ERR: put your discord access token in env.py")

