import random, math, datetime
import numpy as np
from . import params
from .groups import Cancel

class Program():
    def __init__(self, radio):
        self.radio = radio

        self.pre_init()
        self.gen_ngroups() # decide how many groups
        self.gen_library() # generate a song catalog and probablity distrobution
        self.gen_distro()  # generate song probablity distrobution
        self.truncate() # reduce groups based on available songs

        if self.n_groups == 0 :
            raise RuntimeError

        self.gen_groups(self.n_groups)
        
    def pre_init(self):
        pass

    def gen_ngroups(self):
        self.n_groups = random.randrange(self.n[0], self.n[1] + 1)

    def gen_library(self):
        self.library = self.radio.available_tracks()

    def gen_distro(self):
        self.distro = list(np.ones(len(self.library)))

    def truncate(self):
        if self.trunc:
           self.n_groups = min(self.n_groups, math.floor(len(self.library) / self.trunc) )

    def gen_groups(self, n):
        self.groups = []
        grp_types = list(self.g.keys())
        for i in range(n):
            try:
                while grp_types:
                    typ = random.choice(grp_types)
                    rng = self.g[typ]
                    n = random.randrange(rng[0], rng[1]+1)
                    try:
                        group = self.radio.group_types[typ](self.radio, n, self.library, self.distro)
                        break
                    except Cancel as e:
                        e.lib = "{} {}".format(type(self),self)
                        print(e)
                        grp_types.remove(typ)

                if not grp_types:
                    raise RuntimeError

            except Exception as e:
                e.group = typ
                e.program = self
                raise e

            for t in group.playlist:
                i = self.library.index(t)
                self.library.pop(i)
                self.distro.pop(i)
            self.groups.append(group)

    @classmethod
    def gen_weight(cls, history):
        w = cls.w
        if history and history[-1].__class__ == cls:
            w *= cls.b2b
        return w

class ProgramShuffle(Program, params.p_shuffle):
    def __str__(self):
        return "shuffle"

class ProgramPoster(Program, params.p_poster):
    def __str__(self):
        return "user {}".format(self.poster)

    def gen_library(self):
        self.library = self.radio.available_tracks() 

        #1. find artists
        posters = {}
        for t in self.library:
            pls = self.radio.get_posters(t)
            for p in pls:
                if p not in posters:
                    posters[p] = []
                posters[p].append(t)

        #2. filter out less than <trunc> songs
        posters = {k:v for k,v in posters.items() if len(v) >= self.trunc}
        aids = list(posters.keys())

        #3. probablity distro
        weights = [ 1 for aid in aids ] #TODO, inverse bell curve

        #4. pick artist
        self.poster = random.choices(aids,weights)[0]
        self.library = posters[self.poster]

class ProgramArtist(Program, params.p_artist):
    def __str__(self):
        return "artist {}".format(self.radio.artist_name(self.artist))

    def gen_library(self):
        self.library = self.radio.available_tracks() 

        #1. find artists
        artists = {}
        for t in self.library:
            als = self.radio.get_artists(t)
            for a in als:
                if a not in artists:
                    artists[a] = []
                artists[a].append(t)

        #2. filter out less than <trunc> songs
        artists = {k:v for k,v in artists.items() if len(v) >= self.trunc}
        aids = list(artists.keys())

        #3. probablity distro
        weights = [ 1 for aid in aids ] #TODO, inverse bell curve

        #4. pick artist
        self.artist = random.choices(aids,weights)[0]
        self.library = artists[self.artist]

class ProgramRecent(Program, params.p_recent):
    def __str__(self):
        return "new"

    def pre_init(self):
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.days)

    def gen_distro(self):
        pass

    def gen_library(self):
        l_all = self.radio.available_tracks()
        self.library = []
        self.distro = []
        for t in l_all:
            d = max(self.radio.get_dates(t))
            if self.cutoff < d:
                diff = (d - self.cutoff).total_seconds() / (self.days * 24 * 60 * 60)
                diff = 1 - diff
                if diff > 0:
                    self.library.append(t)
                    self.distro.append(diff)

class ProgramThrowback(Program, params.p_throwback):
    def __str__(self):
        return "throwback"

    def pre_init(self):
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.days[0])
        self.taper = datetime.datetime.now() - datetime.timedelta(days=self.days[1])

    def gen_distro(self):
        pass

    def gen_library(self):
        l_all = self.radio.available_tracks()
        self.library = []
        self.distro = []
        for t in l_all:
            d = min(self.radio.get_dates(t))
            if d < self.taper:
                self.library.append(t)
                self.distro.append(1)
            elif d < self.cutoff:
                diff = (self.cutoff - d).total_seconds()
                total = (self.cutoff - self.taper).total_seconds()
                self.library.append(t)
                self.distro.append(diff / total)

class ProgramRange(Program, params.p_range):
    def __str__(self):
        f = "%m/$d/%Y"
        return "{} - {}".format(self.start.strftime(f), self.end.strftime(f))

    def gen_library(self):
        self.library = self.radio.available_tracks()

        t = random.choice(self.library)
        self.center = self.radio.get_dates(t)[0]
        self.start = self.center - datetime.timedelta(days=self.days/2)
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.cutoff)

        self.end   = min(self.cutoff, self.center + datetime.timedelta(days=self.days/2))

    def truncate(self):
        for i in range(20): #max 10 tries
            library = self.filter()
            if len(library) > self.trunc * self.n_groups:
                self.library = library
                self.gen_distro()
                return
            else:
                self.start -= datetime.timedelta(days = self.days/2)
                self.end   += datetime.timedelta(days = self.days/2)
                self.end = min(self.cutoff, self.end)

        raise RuntimeError
        
    def filter(self):
        ls = []
        for t in self.library:
            ds = self.radio.get_dates(t)
            for d in ds:
                if d > self.start and d < self.end:
                    ls.append(t)
        return ls


