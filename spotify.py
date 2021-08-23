import spotipy
from spotipy.oauth2 import SpotifyOAuth

import env

def init():
    scope = 'playlist-modify-public'

    return spotipy.Spotify(auth_manager=SpotifyOAuth(
        client_id     = env.client_id, 
        client_secret = env.client_secret, 
        redirect_uri  = env.redirect_uri,
        scope = scope
    ))

sp = init()

def get_album_info(albums, library):
    #todo use get multible albums
    track_names = {}
    track_artists = {}
    track_lengths = {}
    track_album = {}
    
    artist_names = {}
    
    album_names = {}
    album_tracks = {}

    for batch in batched(albums,15):
        data = sp.albums(batch)
        for a in data['albums']:
            aa = a['uri']
            album_names[aa] = a['name']
            album_tracks[aa] = []
            for track in a['tracks']['items']:
                #this will miss tracks if there are more than 50
                #solution: just dont worry about it
                uri = track['uri']
                album_tracks[aa].append(uri)
                track_names[uri] = track['name']
                track_lengths[uri] = track['duration_ms']
                track_artists[uri] = []
                track_album[uri] = aa
                for artist in track['artists']:
                    a_uri = artist['uri']
                    artist_names[a_uri] = artist['name']
                    track_artists[uri].append(a_uri)

    for uri, name in album_names.items():
        library.albums.add(uri).add_info( 
            name, 
            album_tracks[uri]
        )

    for uri, name in artist_names.items():
        library.artists.add(uri).add_info( name )

    for uri, name in track_names.items():
        library.tracks.add(uri).add_info( 
            name, 
            track_album[uri],
            track_artists[uri],
            track_lengths[uri]
        )
        
    print('fetched metadata on {} albums'.format(len(albums)))
    return locals()

def get_track_album(tracks, library):
    track_names = {}
    track_artists = {}
    track_lengths = {}
    track_album = {}
    
    artist_names = {}

    for batch in batched(tracks,50):
        data = sp.tracks(batch)
        for track in data['tracks']:
            uri = track['uri']
            track_album[uri] = track['album']['uri']

            track_names[uri] = track['name']
            track_lengths[uri] = track['duration_ms']
            track_artists[uri] = []
            for artist in track['artists']:
                a_uri = artist['uri']
                artist_names[a_uri] = artist['name']
                track_artists[uri].append(a_uri)

    for uri, name in track_album.items():
        library.albums.add(track_album[uri]) 

    for uri, name in artist_names.items():
        library.artists.add(uri).add_info( name )

    for uri, name in track_names.items():
        library.tracks.add(uri).add_info( 
            name, 
            track_album[uri],
            track_artists[uri],
            track_lengths[uri]
        )
    print('fetched metadata on {} tracks'.format(len(tracks)))

def fetch_needed(library):
    albums = [ uri for uri,album in library.albums.items() if not album.has_info]
    if albums:
        get_album_info(albums, library)

    tracks = [ uri for uri,track in library.tracks.items() if not track.has_info]
    if tracks:
        # make sure album is in library
        get_track_album(tracks, library)

        # then get any new albums
        albums = [ uri for uri,album in library.albums.items() if not album.has_info]
        if albums:
            get_album_info(albums, library)

def batched(ls, maxn):
    return [ls[i:i + maxn] for i in range(0, len(ls), maxn)]

class PlaylistManager():
    def __init__(self):
        self.playlist = None
        self.get_playlist()

    def get_playlist(self):
        limit = 100
        offset = 0
        ds = {}
        ls = []
        while True:
            data = sp.playlist_items(playlist_id=env.playlist, limit=limit, offset=offset)['items']
            for i,t in enumerate(data):
                uri = t['track']['uri']
                ds[i+offset] = uri 
                ls.append(uri)
            if len(data) != limit:
                break
            offset += limit
        self.playlist = ls

        print("scanned playlist, found {} tracks".format(len(self.playlist)))

    def add_playlist(self,items,position=0):
        for batch in batched(items, 100):
            sp.playlist_add_items(playlist_id=env.playlist, items=batch, position=position)
            position += 100

        print("added {} items to playlist".format(len(items)))

    def rm_playlist(self,items):
        items = [ { 'uri': uri, 'positions': [index] } 
            for uri, index in items ]

        for batch in batched(items, 100):
            # might break if same uri in a batch twice
            sp.playlist_remove_specific_occurrences_of_items(playlist_id=env.playlist, items=batch)
        print("removed {} items from playlist".format(len(items)))
    
    def recreate(self,items):
        sp.playlist_replace_items(env.playlist, [])
        self.add_playlist(items)
        self.get_playlist()
         
    def update(self, items):
        import difflib
        diff = difflib.SequenceMatcher(a=self.playlist, b=items)

        rms = []
        ins = []

        for typ, i1, i2, j1,j2 in diff.get_opcodes():
            if typ == "delete" or typ == "replace":
               for i in range(i1,i2):
                   rms.append((self.playlist[i], i))
                   
            if typ == "insert" or typ == "replace":
               foo = items[j1:j2]
               offset = j1 #insert at correct location after cells removed (must do early cells first)         
               ins.append((foo, offset))

        rms.sort(key = lambda d : d[1], reverse=True)
        ins.sort(key = lambda d : d[1])
        if rms or ins:
            if rms:
                self.rm_playlist(rms)
            for group, offset in ins:
                self.add_playlist(group, position=offset)

            print("sync'd playlist")
            self.get_playlist()
            self.check( items)
        else:
            print("playlist sync'd, no action needed")

    def check(self, items):
        if "".join(self.playlist) == "".join(items):
            print('check passed')
        else:
            print('check FAILED')

#items = ["https://open.spotify.com/album/44FFYALVUH2lNxjYZ5rZqH?si=Tb6KfAoqSMyXk8I9-BFHMw&dl_branch=1"]
#update_playlist(items)


