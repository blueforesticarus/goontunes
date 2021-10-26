package util

import "sync/atomic"

type Output interface {
	Blockable
	Add(Blockable)
}

type Outputs struct {
	list    []Blockable
	counter uint32
}

func (self *Outputs) Add(v Blockable) {
	self.list = append(self.list, v)
}

func (self *Outputs) Set(n int) {
	if n >= 0 {
		atomic.AddUint32(&self.counter, uint32(n))
	} else {
		atomic.AddUint32(&self.counter, ^uint32(-n-1))
	}

	for _, v := range self.list {
		v.Set(n)
	}
}
