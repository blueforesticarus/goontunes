import random, math
from . import params, groups, programs
import datetime

class Play():
    def __init__(self, radio, program, gindex, index):
        self.program = program
        self.gindex = gindex
        self.index = index
        self.group = self.program.groups[self.gindex]
        self.track = self.group.playlist[self.index]
        self.track_name = radio.track_name(self.track)

        self.album = radio.get_album(self.track)
        self.album_name = radio.album_name(self.album)
        self.artists = radio.get_artists(self.track)
        self.artists_names = [ radio.artist_name(a) for a in self.artists ]
        self.date = datetime.datetime.now() # so nothing breaks, overriden on play

    def play(self):
        self.date = datetime.datetime.now()

    def __str__(self):
        a = "{} ({}/{})\n".format(str(self.program), self.gindex+1, self.program.n_groups)
        b = "{} ({}/{})\n".format(str(self.group), self.index+1, self.group.n_tracks)
        c = "{}\n[{}]\nby {}".format(self.track_name, self.album_name, ", ".join(self.artists_names))
        return a + b + c

class Radio():
    program_types = [
        programs.ProgramRecent,
        programs.ProgramRange,
        programs.ProgramThrowback,
        programs.ProgramPoster,
        programs.ProgramArtist,
        programs.ProgramShuffle,
    ]

    group_types = {
        "g_null" : groups.GroupNull,
        "g_album" : groups.GroupAlbum,
        "g_recent" : groups.GroupRecent,
        "g_range" : groups.GroupRange,
        "g_throwback" : groups.GroupThrowback
    }

    def __init__(self, library):
        self.library = library
        self.history = [] #
        self.p_hist = []
        self.restore()

    def save(self):
        import pickle
        with open("radio.pkl", 'wb') as f:
            pickle.dump(self.history,f)
    
    def restore(self):
        import os
        if not os.path.exists("radio.pkl"):
            return

        import pickle
        with open("radio.pkl",'rb') as f:
            try:
                data = pickle.load(f)
            except:
                pass
        self.history = data

    def select(self):
        weights = [p.gen_weight(self.history) for p in self.program_types]
        return random.choices(self.program_types, weights)[0]

    def gen_radio(self):
        while True:
            cls = self.select()
            try:
                program = cls(self)
            except Exception as e:
                import logging, time
                if hasattr(e, "program"):
                    p = e.program
                    g = e.group
                else:
                    p = "?"
                    g = "?"
                logging.exception('%s %s %s failed to initialize', cls, p, g)
                time.sleep(1)
            else:
                self.p_hist.append(program)
                for gn, group in enumerate(program.groups):
                    for tn, track in enumerate(group.playlist):
                        self.history.append( Play(self, program, gn, tn) )
                        self.save()
                        yield self.history[-1]

    def test(self):
        with open('track.hist', 'w') as f:
            for p in self.gen_radio():
                f.write(str(p) + "\n\n")

    def available_tracks(self):
        # returns spotify ids
        backlog = [ play.track for play in self.history[-params.cooldown:] ] 
        
        now = datetime.datetime.now()
        index = 0
        for i in range(len(self.history))[-params.cooldown::-1]:
            p = self.history[i]
            if p.date.day != now.day:
                index = i
                break
        daylog = [ play.track for play in self.history[index+1:] ]
        av= [ uri for uri in self.library.playlist() if uri not in backlog and uri not in daylog] 
        print(len(av), len(self.library.playlist()), len(backlog), len(daylog), len(self.history))
        return av

    def get_album(self, uri):
        return self.library.tracks[uri].album
        
    def get_artists(self, uri):
        return self.library.tracks[uri].artists

    def get_dates(self, uri):
        return [ p.date for p in self.library.tracks[uri].posts.values() ]
        
    def get_posters(self, uri):
        return [ p.poster for p in self.library.tracks[uri].posts.values() ]

    def album_name(self, album):
        return self.library.albums[album].name
    def artist_name(self, artist):
        return self.library.artists[artist].name
    def track_name(self, track):
        return self.library.tracks[track].name
