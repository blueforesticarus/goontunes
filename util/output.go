package util

import (
	"sync/atomic"
)

type Output interface {
	Blockable
	Add(Blockable)
}

type Outputs struct {
	Block
	list []Blockable
}

func (self *Outputs) Add(v Blockable) {
	self.list = append(self.list, v)
}

func (self *Outputs) Set(n int) {
	//NOTE: should we always change internal first?
	//probably... yes, actually

	self.Block.Set(n)

	for _, v := range self.list {
		v.Set(n)
	}
}

/* I don't like having to call initializers*/
type OutputsNeedInit struct {
	Outputs
	initialized int32
}

func (self *OutputsNeedInit) _init() {
	//converts initialized = 0 -> counter+=1 atomically and only once
	if self.initialized != 1 {
		self.Set(1)
		if !atomic.CompareAndSwapInt32(&self.initialized, 0, 1) {
			//some other thread got the swap, undo
			self.Set(-1)
		}
	}
}

func (self *OutputsNeedInit) Wait() int {
	self._init()
	return self.Block.Wait()
}

//special Set(n) which undoes the Set(1) above
//it is therefore an error to call more than once
func (self *OutputsNeedInit) Init(n int) {
	self._init()
	self.Set(n - 1)
}
