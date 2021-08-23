# universal filter
cooldown = 8 * (20) # 10 hrs = 200 songs at avg 20 songs per hour

class p_throwback():
    w = .08
    n = (4,4)

    g = {
        'g_album': (3,3),
        'g_range': (3,5),
        'g_null' : (10,10),
    }
    # probablity distrobution favoring > 2 years, min 1 year
    trunc = 0 #n/a
    b2b = 0

    days = (365, 700)

class p_recent():
    w = .25
    n = (1,3)

    g = {
        'g_album': (3,3),
        'g_null' : (5,5),
    }

    # probablity distrobution favoring very recents
    # point is to guarentee new posts get played
    trunc = 0 #n/a
    b2b = 0

    days = 365

class p_range():
    w = .05
    n = (1,3)

    g = {
        'g_album': (3,3),
        'g_null' : (5,5),
    }

    # restricted to random range, 1 to 6 months
    trunc = 20 #OVERRIDED range expanded to at least 20 songs per group 
    b2b = 1
    days = 14
    cutoff = 30

class p_poster():
    w = .6
    n = (1,4)

    g = {
        'g_album'    : (3,3),
        'g_range'    : (3,5),
        'g_recent'   : (3,3),
        'g_throwback': (4,4),
        'g_null'     : (4,4),
    }

    # restricted to random poster, weighted by number of posts, max weight defined using average posts
    trunc = 10 # truncate 10 songs per group
    b2b = 1

class p_artist():
    w = .12 
    n = (3,3)

    g = {
        'g_album'     : (3,3),
        'g_range'     : (3,5),
        'g_recent'    : (4,4),
        'g_throwback' : (4,4),
        'g_null'      : (4,4),
    }

    # restricted to random artist, weighted by how many songs that artist has on the list
    
    trunc = 5 # truncate 5 songs per group
    b2b = 1

class p_shuffle():
    w = .05
    n = (1,1)

    g = {
        'g_null' : (8,8)
    }

    # true shuffle
    trunc = 0 #n/a
    b2b = 0

class g_range():
    days = 14
    cutoff = 30
    trunc = 30

class g_throwback():
    days = (365,700)

class g_recent():
    days = 200

