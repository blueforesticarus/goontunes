package util

import (
	"sync"
	"sync/atomic"
)

type Blockable interface {
	Set(n int)
}

/*
Go does not have any easy way to wait on two mutexes
and I dont know how to do this with channels

So we implement this,
it can be set and unset, triggering on zero

And we can wait on it.
We can also call trigger manually for stuff which doesn't fit in the model that well.
*/
type Block struct {
	counter uint32
	event   sync.Cond
	lock    sync.Mutex
}

//gay gay gay gay
func (self *Block) Init() {
	self.event.L = &self.lock
}

func (self *Block) Set(n int) {
	self.lock.Lock()
	if n >= 0 {
		atomic.AddUint32(&self.counter, uint32(n))
	} else {
		v := atomic.AddUint32(&self.counter, ^uint32(-n-1))
		if v == 0 {
			self.event.Broadcast()
		} else if v < 0 {
			panic("")
		}
	}
	self.lock.Unlock()
}

func (self *Block) Trigger() {
	self.Init()
	self.lock.Lock()
	self.event.Broadcast()
	self.lock.Unlock()
}

func (self *Block) Wait() int {
	self.Init()
	self.lock.Lock()
	i := atomic.LoadUint32(&self.counter)
	if i != 0 {
		self.event.Wait()
		i = atomic.LoadUint32(&self.counter)
	}
	self.lock.Unlock()
	return int(i)
}

/* maybe remove, not sure it makes code easier to understand */
func (b *Block) Open() {
	b.Set(1)
}

func (b *Block) Close() {
	b.Set(-1)
}

func Open(b Blockable) {
	b.Set(1)
}

func Close(b Blockable) {
	b.Set(-1)
}
