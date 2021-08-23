import random, math, datetime
import numpy as np
from . import params

class Group():
    def __init__(self, radio, n, library, distro):
        self.radio = radio
        self.library = list(library)
        self.distro = list(distro)
        assert len(self.library) == len(self.distro)
        self.n_tracks = n

        self.pre_init()
        self.gen_tracks()

    def pre_init(self):
        pass

    def gen_tracks(self):
        if type(self.distro) is str:
            print(self,self.radio.p_hist[-1])
        p = np.asarray(self.distro).astype('float64')
        p = p / np.sum(p)
        pl = np.random.choice(self.library, size=self.n_tracks, replace=False, p=p)
        self.playlist = list(pl)

class GroupNull(Group):
    def __str__(self):
        return "random"

class GroupAlbum(Group):
    def __str__(self):
        return "album: {}".format(self.radio.album_name(self.album))

    def pre_init(self):
        #1. find albumns
        albums = {}
        distro = {}
        for i,t in enumerate(self.library):
            a = self.radio.get_album(t)
            if a not in albums:
                albums[a] = []
                distro[a] = 0
            albums[a].append(t)
            distro[a] += self.distro[i]

        #2. filter out less than 3 songs
        albums = {k:v for k,v in albums.items() if len(v) >= self.n_tracks}
        aids = list(albums.keys())

        if len(albums) == 0:
            raise Cancel(self)

        #3. probablity distro
        weights = [ distro[aid] / len(albums[aid]) for aid in aids ]

        #4. pick album
        c = random.choices(aids,weights)[0]
        self.album = c

        #5. limit library
        d = []
        for t in albums[c]:
            i = self.library.index(t)
            d.append(self.distro[i])

        self.distro = d
        self.library = albums[c]


class GroupRange(Group, params.g_range):
    def __str__(self):
        f = "%m/$d/%Y"
        return "{} - {}".format(self.start.strftime(f), self.end.strftime(f))

    def pre_init(self):
        t = random.choices(self.library, self.distro)[0]
        self.center = self.radio.get_dates(t)[0]
        self.start = self.center - datetime.timedelta(days=self.days/2)
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.cutoff)

        self.end   = min(self.cutoff, self.center + datetime.timedelta(days=self.days/2))

        for i in range(10): #max 10 tries
            library,distro = self.filter()
            if len(library) > self.trunc:
                self.library = library
                self.distro = distro
                return
            else:
                self.start -= datetime.timedelta(days = self.days/2)
                self.end   += datetime.timedelta(days = self.days/2)
                self.end = min(self.cutoff, self.end)


        if len(self.library) < self.n_tracks + 5:
            raise Cancel(self)
        
    def filter(self):
        ls = []
        distro = []
        for i,t in enumerate(self.library):
            ds = self.radio.get_dates(t)
            for d in ds:
                if d > self.start and d < self.end:
                    ls.append(t)
                    distro.append(self.distro[i])
                    break

        return ls, distro

class GroupRecent(Group, params.g_recent):
    def __str__(self):
        return "new"

    def pre_init(self):
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.days)

        library = []
        distro = []
        for i,t in enumerate(self.library):
            d = max(self.radio.get_dates(t))
            if self.cutoff < d:
                diff = (d - self.cutoff).total_seconds() / (self.days * 24 * 60 * 60)
                diff = 1 - diff
                if diff > 0:
                    library.append(t)
                    distro.append(diff * self.distro[i])
        self.library = library
        self.distro = distro

        if len(self.library) < self.n_tracks + 5:
            raise Cancel(self)

class GroupThrowback(Group, params.g_throwback):
    def __str__(self):
        return "throwback"

    def pre_init(self):
        self.cutoff = datetime.datetime.now() - datetime.timedelta(days=self.days[0])
        self.taper = datetime.datetime.now() - datetime.timedelta(days=self.days[1])

        library = []
        distro = []
        for i, t in enumerate(self.library):
            d = min(self.radio.get_dates(t))
            if d < self.taper:
                library.append(t)
                distro.append(self.distro[i])
            elif d < self.cutoff:
                diff = (self.cutoff - d).total_seconds()
                total = (self.cutoff - self.taper).total_seconds()
                library.append(t)
                distro.append(self.distro[i] * diff / total)
        self.library = library
        self.distro = distro
        
        if len(self.library) < self.n_tracks + 5:
            raise Cancel(self)

class Cancel(Exception):
    def __init__(self, grp):
        self.grp = grp
        self.lib = "unknown"
    def __str__(self):
        return "canceled [{}] library:{} group:{}".format(len(self.grp.library),self.lib,type(self.grp))
